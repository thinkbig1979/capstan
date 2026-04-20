import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { resourcesApi } from '@/lib/api'
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
    queryFn: resourcesApi.listBuildCache,
    retry: 1,
  })
}

export function useCheckUpdates() {
  return useQuery({
    queryKey: ['resources', 'updates'],
    queryFn: () => resourcesApi.checkUpdates(false),
    enabled: false,
    retry: 1,
    staleTime: 60000,
  })
}

export function useCheckUpdatesRefresh() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => resourcesApi.checkUpdates(true),
    onSuccess: (data) => {
      queryClient.setQueryData(['resources', 'updates'], data)
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
    queryFn: () => resourcesApi.getAutoUpdatePolicies(),
    retry: 1,
  })
}

export function useToggleAutoUpdate() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ targetType, targetId, enabled }: { targetType: string; targetId: string; enabled: boolean }) =>
      resourcesApi.setAutoUpdatePolicy(targetType, targetId, { enabled }),
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
    queryFn: () => resourcesApi.getUpdateSettings(),
    retry: 1,
  })
}

export function useUpdateUpdateSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { scanIntervalMinutes: number; globalAutoUpdate: boolean }) =>
      resourcesApi.updateUpdateSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings', 'updates'] })
    },
  })
}

export function useGitSettings() {
  return useQuery({
    queryKey: ['settings', 'git'],
    queryFn: () => resourcesApi.getGitSettings(),
    retry: 1,
  })
}

export function useUpdateGitSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { sshKey?: string; httpsUser?: string; httpsToken?: string }) =>
      resourcesApi.updateGitSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings', 'git'] })
    },
  })
}
