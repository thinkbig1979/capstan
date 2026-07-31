import { useQuery } from '@tanstack/react-query'
import { versionApi } from '@/lib/api'
import { queryKeys } from '@/lib/query-keys'

/**
 * Build identity of the running backend.
 *
 * The values are stamped into the binary at link time, so they cannot change
 * while the process is up: staleTime is Infinity and there is no refetch. A
 * reload after a `docker compose pull` picks up the new build.
 */
export function useVersion() {
  return useQuery({
    queryKey: queryKeys.version(),
    queryFn: () => versionApi.get(),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: 1,
  })
}
