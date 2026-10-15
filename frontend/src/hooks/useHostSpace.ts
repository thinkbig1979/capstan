import { useQuery } from '@tanstack/react-query'
import { resourcesApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'

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
