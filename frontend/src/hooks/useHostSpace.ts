import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { resourcesApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'
import { useActionMutation } from '@/hooks/useActionMutation'
import type { DockerCleanupCategories } from '@/types'
import type { HostFreeSpaceRequest } from '@/types/generated-services'

/** How often the Docker host numbers refresh while something shows them. */
const HOST_SUMMARY_REFETCH_MS = 60_000

/**
 * The Docker host summary (GET /resources/host/summary). The server caches it
 * for 30 s, so a 60 s poll costs at most one Docker read per minute; a
 * resource_changed event invalidates it sooner (useStackEvents).
 */
export function useHostSummary() {
  return useQuery({
    queryKey: queryKeys.resources.hostSummary(),
    queryFn: resourcesApi.hostSummary,
    refetchInterval: HOST_SUMMARY_REFETCH_MS,
  })
}

/**
 * What the Free-up-space dialog lists. staleTime 0 and gcTime 0: the user is
 * about to delete from this list, so every opening reads it fresh.
 */
export function useHostCandidates(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.resources.hostCandidates(),
    queryFn: resourcesApi.hostCandidates,
    enabled,
    staleTime: 0,
    gcTime: 0,
  })
}

/** Removes the ticked items. The toast reads the ActionResult (safe-defaults rule 8). */
export function useFreeSpace() {
  return useActionMutation({
    mutationFn: (req: HostFreeSpaceRequest) => resourcesApi.freeSpace(req),
    errorTitle: 'Could not free up space',
    invalidate: [
      queryKeys.resources.hostSummary(),
      queryKeys.resources.hostCandidates(),
      queryKeys.dashboardStats(),
      queryKeys.resources.images(),
      queryKeys.resources.volumes(),
      queryKeys.resources.networks(),
      queryKeys.resources.buildCache(),
      queryKeys.resources.cleanupPreview(),
    ],
  })
}

/**
 * Turns automatic cleanup on for the given categories. The PUT merges over the
 * stored policy (updateCleanupPolicy in handlers/docker_cleanup.go writes only
 * the keys sent), so sending only `true` keys adds and never turns one off.
 */
export function useAddAutoCleanupCategories() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (categories: Partial<DockerCleanupCategories>) =>
      resourcesApi.updateCleanupPolicy({ enabled: true, categories }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.dockerCleanup() })
      queryClient.invalidateQueries({ queryKey: queryKeys.resources.cleanupPreview() })
    },
  })
}
