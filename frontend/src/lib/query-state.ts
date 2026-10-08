import type { FetchStatus } from '@tanstack/react-query'

/**
 * True while a query that IS enabled has no data yet: fetching, or paused
 * because the browser is offline (agent-os-7nqa).
 *
 * Use this, not `isLoading`, to gate a first-load spinner on a query with
 * `enabled: ...`. `isLoading` is `isPending && isFetching`, so offline (the
 * first fetch is 'paused') it is false with no data, and the page falls through
 * to an error, "not found" or empty-state claim about a read that never ran.
 * Plain `isPending` is not an option for a gated query either: a DISABLED query
 * stays pending forever, so it would spin where the page must fall through.
 * A disabled query's fetchStatus is 'idle', which is what this excludes.
 *
 * For a query with no `enabled`, plain `isPending` is the whole answer.
 */
export function isFirstLoad({
  isPending,
  fetchStatus,
}: {
  isPending: boolean
  fetchStatus: FetchStatus
}): boolean {
  return isPending && fetchStatus !== 'idle'
}
