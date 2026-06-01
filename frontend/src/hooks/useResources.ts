import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { toast } from 'sonner'
import { resourcesApi, settingsApi, autoUpdateApi } from '@/lib/api'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import type { UpdateHistoryFilters } from '@/types'

// Shared sonner id so the loading toast is replaced (not stacked) on completion.
export const UPDATE_SCAN_TOAST_ID = 'update-scan'

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
  const finishScan = useUpdateScanStore((s) => s.finishScan)

  const query = useQuery({
    queryKey: ['resources', 'updates'],
    queryFn: () => resourcesApi.checkUpdates(false),
    enabled: true,
    refetchInterval: isScanning ? 3000 : false,
    staleTime: 60000,
  })

  useEffect(() => {
    if (query.data?.scanning && !isScanning) startScan()
    else if (query.data && !query.data.scanning && isScanning) finishScan()
  }, [query.data?.scanning, isScanning, startScan, finishScan])

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

  const { data } = useQuery({
    queryKey: ['resources', 'updates'],
    queryFn: () => resourcesApi.checkUpdates(false),
    enabled: isScanning,
    refetchInterval: isScanning ? 3000 : false,
    staleTime: 60000,
  })

  // Toast only on the true<->false transitions so it doesn't re-fire on every poll.
  useEffect(() => {
    if (isScanning && !wasScanning.current) {
      toast.loading('Checking for updates…', { id: UPDATE_SCAN_TOAST_ID })
    } else if (!isScanning && wasScanning.current) {
      toast.success('Update check complete', { id: UPDATE_SCAN_TOAST_ID, duration: 3000 })
    }
    wasScanning.current = isScanning
  }, [isScanning])

  // A poll reporting scanning:false means the backend scan finished — clear the
  // shared state and cache the fresh results even if the Updates tab is unmounted.
  useEffect(() => {
    if (isScanning && data && !data.scanning) {
      queryClient.setQueryData(['resources', 'updates'], data)
      finishScan()
    }
  }, [isScanning, data, finishScan, queryClient])
}

export function useCheckUpdatesRefresh() {
  const queryClient = useQueryClient()
  const startScan = useUpdateScanStore((s) => s.startScan)
  const finishScan = useUpdateScanStore((s) => s.finishScan)
  return useMutation({
    mutationFn: async () => {
      startScan()
      const data = await resourcesApi.checkUpdates(true)
      if (data.scanning) {
        return data
      }
      finishScan()
      return data
    },
    onSuccess: (data) => {
      if (!data.scanning) {
        queryClient.setQueryData(['resources', 'updates'], data)
      }
    },
    onError: () => {
      finishScan()
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
