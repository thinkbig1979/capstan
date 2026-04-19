import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { resourcesApi } from '@/lib/api'

export function useImages() {
  return useQuery({
    queryKey: ['resources', 'images'],
    queryFn: resourcesApi.images,
    refetchInterval: 30000,
    retry: 1,
  })
}

export function useVolumes() {
  return useQuery({
    queryKey: ['resources', 'volumes'],
    queryFn: resourcesApi.volumes,
    refetchInterval: 30000,
    retry: 1,
  })
}

export function useNetworks() {
  return useQuery({
    queryKey: ['resources', 'networks'],
    queryFn: resourcesApi.networks,
    refetchInterval: 30000,
    retry: 1,
  })
}

export function useBuildCache() {
  return useQuery({
    queryKey: ['resources', 'build-cache'],
    queryFn: resourcesApi.listBuildCache,
    refetchInterval: 30000,
    retry: 1,
  })
}

export function useCheckUpdates() {
  return useQuery({
    queryKey: ['resources', 'updates'],
    queryFn: resourcesApi.checkUpdates,
    enabled: false,
    retry: 1,
    staleTime: 60000,
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
    },
  })
}
