import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
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
