import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { toast } from 'sonner'
import { resourcesApi, settingsApi, autoUpdateApi } from '@/lib/api'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import type { UpdateHistoryFilters } from '@/types'

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

export function resolveUpdateScanError() {
  const store = useUpdateScanStore.getState()
  if (!store.isScanning) return
  store.finishScan()
  toast.error('Update check failed', { id: UPDATE_SCAN_TOAST_ID, duration: 4000 })
}

export function useImages() {
  return useQuery({
    queryKey: ['resources', 'images'],
    queryFn: resourcesApi.images,
    retry: 1,
  })
}

export function useVolumes() {
  return useQuery({
    queryKey: ['resources', 'volumes'],
    queryFn: resourcesApi.volumes,
    retry: 1,
  })
}

export function useNetworks() {
  return useQuery({
    queryKey: ['resources', 'networks'],
    queryFn: resourcesApi.networks,
    retry: 1,
  })
}

export function useBuildCache() {
  return useQuery({
    queryKey: ['resources', 'build-cache'],
    queryFn: resourcesApi.buildCache,
    retry: 1,
  })
}

export function useCheckUpdates() {
  const isScanning = useUpdateScanStore((s) => s.isScanning)
  const startScan = useUpdateScanStore((s) => s.startScan)

  const query = useQuery({
    queryKey: ['resources', 'updates'],
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
    queryKey: ['resources', 'updates'],
    queryFn: () => resourcesApi.checkUpdates(false),
    enabled: isScanning,
    refetchInterval: isScanning ? 3000 : false,
    staleTime: 0,
  })

  // On scan start: record the pre-scan scannedAt baseline and show the loading toast.
  useEffect(() => {
    if (isScanning && !wasScanning.current) {
      const cached = queryClient.getQueryData<{ scannedAt?: string }>(['resources', 'updates'])
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
        queryClient.setQueryData(['resources', 'updates'], data)
        resolveUpdateScanSuccess()
      }
    },
    onError: () => {
      resolveUpdateScanError()
    },
  })
}

export function useUpdateContainer() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (containerId: string) => resourcesApi.updateContainer(containerId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['resources', 'updates'] })
      queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] })
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['update-history'] })
    },
  })
}

export function useUpdateHistory(filters: UpdateHistoryFilters) {
  return useQuery({
    queryKey: ['update-history', filters],
    queryFn: () => resourcesApi.getUpdateHistory(filters),
    retry: 1,
  })
}

export function useAutoUpdatePolicies() {
  return useQuery({
    queryKey: ['auto-update-policies'],
    queryFn: () => autoUpdateApi.getPolicies(),
    retry: 1,
  })
}

export function useToggleAutoUpdate() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ targetType, targetId, enabled }: { targetType: string; targetId: string; enabled: boolean }) =>
      autoUpdateApi.setPolicy(targetType, targetId, { enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auto-update-policies'] })
      queryClient.invalidateQueries({ queryKey: ['resources', 'updates'] })
      queryClient.invalidateQueries({ queryKey: ['settings', 'updates'] })
    },
  })
}

export function useUpdateSettings() {
  return useQuery({
    queryKey: ['settings', 'updates'],
    queryFn: () => settingsApi.getUpdates(),
    retry: 1,
  })
}

export function useUpdateUpdateSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { scanIntervalMinutes: number; globalAutoUpdate: boolean }) =>
      settingsApi.updateUpdates(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings', 'updates'] })
    },
  })
}

export function useGitSettings() {
  return useQuery({
    queryKey: ['settings', 'git'],
    queryFn: () => settingsApi.getGit(),
    retry: 1,
  })
}

export function useGlobalEnv() {
  return useQuery({
    queryKey: ['settings', 'global-env'],
    queryFn: () => settingsApi.getGlobalEnv(),
    retry: 1,
  })
}

export function useUpdateGlobalEnv() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (vars: Array<{ key: string; value: string }>) =>
      settingsApi.updateGlobalEnv(vars),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings', 'global-env'] })
    },
  })
}

export function useUpdateGitSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { sshKey?: string; httpsUser?: string; httpsToken?: string }) =>
      settingsApi.updateGit(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings', 'git'] })
    },
  })
}
