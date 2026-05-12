import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { resourcesApi, settingsApi, autoUpdateApi } from '@/lib/api'
import { useUpdateScanStore } from '@/stores/updateScanStore'
import type { UpdateHistoryFilters } from '@/types'

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
  const { isScanning, finishScan, startScan } = useUpdateScanStore()
  return useQuery({
    queryKey: ['resources', 'updates'],
    queryFn: async () => {
      const data = await resourcesApi.checkUpdates(false)
      if (data.scanning && !isScanning) {
        startScan()
      } else if (!data.scanning && isScanning) {
        finishScan()
      }
      return data
    },
    enabled: true,
    refetchInterval: isScanning ? 3000 : false,
    staleTime: 60000,
  })
}

export function useCheckUpdatesRefresh() {
  const queryClient = useQueryClient()
  const { startScan, finishScan } = useUpdateScanStore()
  return useMutation({
    mutationFn: async () => {
      startScan()
      const data = await resourcesApi.checkUpdates(true)
      if (data.status === 'scanning') {
        return data
      }
      finishScan()
      return data
    },
    onSuccess: (data) => {
      if (data.status !== 'scanning') {
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
