import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { toast } from 'sonner'
import { resourcesApi, settingsApi, autoUpdateApi, type PruneOptions } from '@/lib/api'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import { isActionResult, toastForResult, type ActionResult } from '@/lib/action-result'
import { classifyError } from '@/lib/error-handler'
import type { UpdateHistoryFilters } from '@/types'
import { queryKeys } from '@/lib/query-keys'
import { messageOrNull, stringOr } from '@/lib/narrow'

// Shared sonner id so the loading toast is replaced (not stacked) on completion.
export const UPDATE_SCAN_TOAST_ID = 'update-scan'

// Completion is resolved from two independent signals: the fast update_scan_complete
// WS event (useStackEvents) and the reliable scannedAt-changed poll (useUpdateScanWatcher).
// Both funnel through these helpers, which are gated on isScanning so whichever fires
// first wins and the other becomes a no-op — no double toast, no premature finish.
export function resolveUpdateScanSuccess() {
  const store = useUpdateScanStore.getState()
  if (!store.isScanning) return
  store.finishScan()
  toast.success('Update check complete', { id: UPDATE_SCAN_TOAST_ID, duration: 3000 })
}

/**
 * The update scan's ONE actionable wire code, 503 DOCKER_UNAVAILABLE, carrying
 * DockerUnavailableMessage (backend respond.go:186): "Docker daemon
 * unreachable: the server started without a usable Docker connection. Check
 * that the Docker socket is mounted and the daemon is running, then restart
 * Capstan." That is a cause and a recovery, where 'Update check failed' reads
 * to an operator as "no updates were found".
 *
 * WHAT THIS DOES NOT COVER, stated so the next reader does not believe
 * otherwise: the 503 is reachable ONLY when Capstan BOOTED without Docker.
 * GET /resources/updates?refresh=true returns 202 Accepted whenever the
 * scheduler exists (checkUpdates, updates.go:36; the scheduler branch is
 * entered at :40 and its c.JSON(http.StatusAccepted, ...) is at :82), and the
 * scheduler is built iff the Docker service is (main.go:379-382). So if Docker was up at boot and the
 * daemon dies later, the refresh still answers 202 and the failure arrives on
 * the WS update_scan_failed event, which carries no cause at all — see
 * resolveUpdateScanError's caller in useStackEvents. This function changes
 * nothing for that case. The scan's reason in that world is served on the
 * update-settings query instead (agent-os-xhn6).
 *
 * Keys on the CODE and renders only `message`. Deliberately NOT routed through
 * classifyError(), which is the idiom everywhere else in this file. The
 * original reason no longer holds: that arm discarded `message` and answered
 * "503: Something went wrong on the server", and agent-os-mc4i made it render
 * the cause. What remains is narrower but still decides it — this returns the
 * bare sentence for a toast DESCRIPTION, where classifyError's 5xx arm prefixes
 * the status (the status being diagnostic is why it keeps it), and keying on
 * DOCKER_UNAVAILABLE is what makes this fire for that outage and nothing else.
 */
function updateScanFault(error: unknown): string | null {
  if (!error || typeof error !== 'object') return null
  const body = error as { code?: string; message?: unknown }
  if (body.code !== 'DOCKER_UNAVAILABLE') return null
  // agent-os-06c1: narrowed, not asserted. This lands in sonner's
  // `description`, typed ReactNode, inside the Toaster subtree.
  return messageOrNull(body.message)
}

/**
 * `error` is OPTIONAL because this function has two callers and only one of
 * them has an error to give it. The HTTP caller (useCheckUpdatesRefresh below)
 * passes the rejected response; the WS caller
 * (useStackEvents' handleUpdateScanFailedEvent) is a message handler whose
 * payload is `{ type, timestamp }` and nothing else, so it passes nothing and
 * gets the generic sentence. That is correct and expected rather than a
 * shortfall: the emitter (services/scheduler.go:342) HOLDS the error — it
 * writes err.Error() into the update_scan_last_error setting a few lines
 * earlier — and ships only the type and the timestamp, a deliberate
 * server-side decision. Making the WS caller invent a cause would fabricate
 * one, which is the whole defect this line of work exists to stop.
 *
 * The parameter goes on the SHARED function rather than the cause being read
 * at the call site because this is a store transition, not a toast: it is
 * gated on isScanning and calls finishScan(). Reading the cause at the call
 * site would mean duplicating that body, or skipping finishScan() and
 * stranding isScanning=true with the loading toast unresolved until the
 * watcher's safety net.
 */
export function resolveUpdateScanError(error?: unknown) {
  const store = useUpdateScanStore.getState()
  if (!store.isScanning) return
  store.finishScan()
  const cause = updateScanFault(error)
  const options = { id: UPDATE_SCAN_TOAST_ID, duration: 4000 }
  toast.error('Update check failed', cause ? { ...options, description: cause } : options)
}

export function useImages() {
  return useQuery({
    queryKey: queryKeys.resources.images(),
    queryFn: resourcesApi.images,
  })
}

export function useVolumes() {
  return useQuery({
    queryKey: queryKeys.resources.volumes(),
    queryFn: resourcesApi.volumes,
  })
}

export function useNetworks() {
  return useQuery({
    queryKey: queryKeys.resources.networks(),
    queryFn: resourcesApi.networks,
  })
}

export function useBuildCache() {
  return useQuery({
    queryKey: queryKeys.resources.buildCache(),
    queryFn: resourcesApi.buildCache,
  })
}

// ─── Resource mutation hooks (B3) ─────────────────────────────────────────────
//
// The backend always returns an Action Truth Contract body
// ({outcome, reason, details}), so `toastForResult` drives the correct toast
// level from the outcome. The isActionResult() guard remains as a type-narrowing
// gate over the api wire union, not a runtime legacy fallback.

export function useDeleteImage() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, force }: { id: string; force: boolean }) =>
      resourcesApi.deleteImage(id, force),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        // outcome drives the toast:
        // no_change/partial = untagged-only (image still referenced) → info/warning
        // success = fully deleted → success (green)
        toastForResult(data, { successTitle: 'Image removed' })
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.images() })
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to remove image')
      }
    },
  })
}

export function useDeleteVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ name, force }: { name: string; force: boolean }) =>
      resourcesApi.deleteVolume(name, force),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        toastForResult(data, { successTitle: 'Volume removed' })
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.volumes() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to remove volume')
      }
    },
  })
}

export function useDeleteNetwork() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => resourcesApi.deleteNetwork(id),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        toastForResult(data, { successTitle: 'Network removed' })
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.networks() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to remove network')
      }
    },
  })
}

export function useCreateNetwork() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: { name: string; driver?: string; internal?: boolean; attachable?: boolean }) =>
      resourcesApi.createNetwork(input),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        // details.name is set by the backend (createNetwork returns {id, name} in details)
        // agent-os-06c1: narrowed, not asserted. `?? 'unknown'` only ever
        // caught an ABSENT name; a non-string one rendered as [object Object].
        const networkName = stringOr((data.details as { name?: unknown } | undefined)?.name, 'unknown')
        toast.success(`Network "${networkName}" created`)
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.networks() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to create network')
      }
    },
  })
}

/**
 * Derives a human-readable prune summary from an Action Truth Contract result.
 *
 * Backend detail key alignment (confirmed from resource_mutations.go):
 *  - Image prune: details.imagesDeleted (number), details.spaceReclaimed
 *  - Volume/container/build-cache prune: details.deleted (array), details.spaceReclaimed
 *  - Network prune: details.deleted (array)
 */
export function resolvePruneSummary(
  data: ActionResult<{
    // Image prune field (classifyImagePruneReport)
    imagesDeleted?: number
    tagsRemoved?: number
    // Generic list field (volume/network/build-cache prune)
    deleted?: string[]
    // Shared space field
    spaceReclaimed?: number
  }>,
  resourceLabel: string,
): string {
  const details = data.details
  // Image prune uses imagesDeleted; others use deleted.length
  const count = details?.imagesDeleted ?? details?.deleted?.length ?? 0
  const space = details?.spaceReclaimed
  const tags = details?.tagsRemoved ?? 0
  let label = `${count} ${resourceLabel}${count !== 1 ? 's' : ''}`
  // Image prune that only removed tags (no full images) would otherwise show
  // "0 images" — surface the tags so the toast reflects the real effect (B3).
  if (tags > 0) {
    label += `, ${tags} tag${tags !== 1 ? 's' : ''}`
  }
  return space ? `${label}, ${formatPruneBytes(space)} reclaimed` : label
}

function formatPruneBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / k ** i).toFixed(1))} ${sizes[i]}`
}

export function usePruneImages() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (opts?: PruneOptions) => resourcesApi.pruneImages(opts),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        if (data.outcome === 'no_change') {
          toast.info(data.reason || 'No images to prune')
        } else {
          const summary = resolvePruneSummary(data, 'image')
          toastForResult(data, { successTitle: `Pruned ${summary}` })
        }
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.images() })
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to prune images')
      }
    },
  })
}

export function usePruneVolumes() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (opts?: PruneOptions) => resourcesApi.pruneVolumes(opts),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        if (data.outcome === 'no_change') {
          toast.info(data.reason || 'No volumes to prune')
        } else {
          const summary = resolvePruneSummary(data, 'volume')
          toastForResult(data, { successTitle: `Pruned ${summary}` })
        }
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.volumes() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to prune volumes')
      }
    },
  })
}

export function usePruneNetworks() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (opts?: PruneOptions) => resourcesApi.pruneNetworks(opts),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        if (data.outcome === 'no_change') {
          toast.info(data.reason || 'No networks to prune')
        } else {
          const details = data.details as { deleted?: string[] } | undefined
          const count = details?.deleted?.length ?? 0
          toastForResult(data, { successTitle: `Pruned ${count} network${count !== 1 ? 's' : ''}` })
        }
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.networks() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to prune networks')
      }
    },
  })
}

export function usePruneBuildCache() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (opts?: PruneOptions) => resourcesApi.pruneBuildCache(opts),
    onSuccess: (data) => {
      if (isActionResult(data)) {
        if (data.outcome === 'no_change') {
          toast.info(data.reason || 'No build cache to prune')
        } else {
          const summary = resolvePruneSummary(data, 'cache entry')
          toastForResult(data, { successTitle: `Pruned ${summary}` })
        }
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.buildCache() })
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
    },
    onError: (err) => {
      if (isActionResult(err)) {
        toastForResult(err)
      } else {
        toast.error(classifyError(err).message || 'Failed to prune build cache')
      }
    },
  })
}

export function useCheckUpdates() {
  const isScanning = useUpdateScanStore((s) => s.isScanning)
  const startScan = useUpdateScanStore((s) => s.startScan)

  const query = useQuery({
    queryKey: queryKeys.resources.updates(),
    queryFn: () => resourcesApi.checkUpdates(false),
    enabled: true,
    refetchInterval: isScanning ? 3000 : false,
    staleTime: 60000,
  })

  // Surface the indicator when a scan is already running (e.g. a scheduled
  // background scan). This hook only ever STARTS a scan; it never ends one, because
  // a bare scanning:false can be stale cache and would finish prematurely. Ending a
  // scan is owned by useUpdateScanWatcher (scannedAt poll) and the WS event.
  useEffect(() => {
    if (query.data?.scanning && !isScanning) startScan()
  }, [query.data?.scanning, isScanning, startScan])

  return query
}

// useUpdateScanWatcher gives the update check an app-wide presence. The Updates
// tab's own in-progress card disappears the moment you navigate away, so a scan
// that's still running becomes invisible. Mounted once in the persistent layout,
// this hook keeps polling while a scan is in flight (so the state clears even
// when the tab is unmounted) and shows a global toast that survives navigation.
export function useUpdateScanWatcher() {
  const isScanning = useUpdateScanStore((s) => s.isScanning)
  const finishScan = useUpdateScanStore((s) => s.finishScan)
  const queryClient = useQueryClient()
  const wasScanning = useRef(false)
  // The scannedAt value from BEFORE the scan started. A genuine completion bumps it
  // to a newer timestamp (the backend writes update_scan_last_run, then broadcasts
  // update_scan_complete). Stale cached polls carry this same baseline, so comparing
  // against it is what eliminates the premature-finish race.
  const baselineScannedAt = useRef<string | null>(null)

  // Poll while a scan is in flight: staleTime 0 + refetchInterval guarantees fresh
  // server data every few seconds so we observe the new scannedAt promptly.
  const { data } = useQuery({
    queryKey: queryKeys.resources.updates(),
    queryFn: () => resourcesApi.checkUpdates(false),
    enabled: isScanning,
    refetchInterval: isScanning ? 3000 : false,
    staleTime: 0,
  })

  // On scan start: record the pre-scan scannedAt baseline and show the loading toast.
  useEffect(() => {
    if (isScanning && !wasScanning.current) {
      const cached = queryClient.getQueryData<{ scannedAt?: string }>(queryKeys.resources.updates())
      baselineScannedAt.current = cached?.scannedAt ?? null
      toast.loading('Checking for updates…', { id: UPDATE_SCAN_TOAST_ID })
    }
    wasScanning.current = isScanning
  }, [isScanning, queryClient])

  // Reliable, WS-independent completion: a fresh poll reporting scanning:false AND a
  // scannedAt newer than the baseline means the scan genuinely finished. This is the
  // primary completion path; the WS event (useStackEvents) just resolves it faster.
  useEffect(() => {
    if (!isScanning || !data || data.scanning) return
    if (data.scannedAt && data.scannedAt !== baselineScannedAt.current) {
      resolveUpdateScanSuccess()
    }
  }, [isScanning, data])

  // Safety net: if polling itself stalls (network errors) and no WS event arrives,
  // don't strand the spinner forever. Matches the backend RunScan 10-minute cap.
  useEffect(() => {
    if (!isScanning) return
    const timeout = setTimeout(() => {
      toast.dismiss(UPDATE_SCAN_TOAST_ID)
      finishScan()
    }, 11 * 60 * 1000)
    return () => clearTimeout(timeout)
  }, [isScanning, finishScan])
}

export function useCheckUpdatesRefresh() {
  const queryClient = useQueryClient()
  const startScan = useUpdateScanStore((s) => s.startScan)
  return useMutation({
    mutationFn: async () => {
      startScan()
      return resourcesApi.checkUpdates(true)
    },
    onSuccess: (data) => {
      // scanning:true is the normal path — the scan runs in the background and the
      // watcher poll / WS event resolves it. scanning:false only happens on the
      // synchronous no-scheduler path, where the scan is already done.
      if (!data.scanning) {
        queryClient.setQueryData(queryKeys.resources.updates(), data)
        resolveUpdateScanSuccess()
      }
    },
    // Takes the error. It used to take nothing, so the 503's message was
    // unreadable in principle rather than merely unread — no unused variable
    // and no type error to notice (agent-os-82lk).
    onError: (error) => {
      resolveUpdateScanError(error)
    },
  })
}

export function useUpdateContainer() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (containerId: string) => resourcesApi.updateContainer(containerId),
    onSuccess: (data, containerId) => {
      if (data.jobId) {
        useUpdateJobStore.getState().upsertJob({
          id: data.jobId,
          targetType: 'container',
          targetId: containerId,
          name: containerId,
          stackId: '',
          status: 'queued',
          lines: [],
          createdAt: new Date().toISOString(),
        })
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.updates() })
      queryClient.invalidateQueries({ queryKey: queryKeys.dashboardStats() })
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks() })
      queryClient.invalidateQueries({ queryKey: queryKeys.updateHistory.all() })
    },
  })
}

export function useUpdateStack() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (stackId: string) => resourcesApi.updateStack(stackId),
    onSuccess: (data, stackId) => {
      if (data.jobId) {
        useUpdateJobStore.getState().upsertJob({
          id: data.jobId,
          targetType: 'stack',
          targetId: stackId,
          name: stackId,
          stackId: stackId,
          status: 'queued',
          lines: [],
          createdAt: new Date().toISOString(),
        })
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.updates() })
    },
  })
}

export function useUpdateJobs() {
  return useQuery({
    queryKey: queryKeys.resources.updateJobs(),
    queryFn: async () => {
      const data = await resourcesApi.getUpdateJobs()
      useUpdateJobStore.getState().hydrate(data.jobs)
      return data
    },
  })
}

export function useUpdateHistory(filters: UpdateHistoryFilters) {
  return useQuery({
    queryKey: queryKeys.updateHistory.list(filters),
    queryFn: () => resourcesApi.getUpdateHistory(filters),
  })
}

export function useAutoUpdatePolicies() {
  return useQuery({
    queryKey: queryKeys.autoUpdatePolicies(),
    queryFn: () => autoUpdateApi.getPolicies(),
  })
}

export function useToggleAutoUpdate() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ targetType, targetId, enabled }: { targetType: string; targetId: string; enabled: boolean }) =>
      autoUpdateApi.setPolicy(targetType, targetId, { enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.autoUpdatePolicies() })
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.updates() })
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.updates() })
    },
  })
}

export function useUpdateSettings() {
  return useQuery({
    queryKey: queryKeys.settings.updates(),
    queryFn: () => settingsApi.getUpdates(),
  })
}

export function useUpdateUpdateSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { scanIntervalMinutes: number; globalAutoUpdate: boolean; applyMode?: 'immediate' | 'scheduled'; applyTime?: string; applyDays?: number[] }) =>
      settingsApi.updateUpdates(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.updates() })
    },
  })
}

export function useGitSettings() {
  return useQuery({
    queryKey: queryKeys.settings.git(),
    queryFn: () => settingsApi.getGit(),
  })
}

export function useGlobalEnv() {
  return useQuery({
    queryKey: queryKeys.settings.globalEnv(),
    queryFn: () => settingsApi.getGlobalEnv(),
  })
}

export function useUpdateGlobalEnv() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (vars: Array<{ key: string; value: string }>) =>
      settingsApi.updateGlobalEnv(vars),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() })
    },
  })
}

export function useRetentionSettings() {
  return useQuery({
    queryKey: queryKeys.settings.retention(),
    queryFn: () => settingsApi.getRetention(),
  })
}

export function useUpdateRetentionSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: {
      retentionDays?: number
      updateHistoryRetentionDays?: number
      backupHistoryRetentionDays?: number
    }) => settingsApi.updateRetention(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.retention() })
    },
  })
}

export function useUpdateGitSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { sshKey?: string; httpsUser?: string; httpsToken?: string }) =>
      settingsApi.updateGit(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.git() })
    },
  })
}
