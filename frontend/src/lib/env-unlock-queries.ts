import { partialMatchKey, type Query, type QueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/query-keys'

/**
 * Drops or refetches every query the backend redacts without an unlock token.
 *
 * Called on both edges of the unlock window, for opposite reasons: on unlock the
 * cache holds blanked secrets that must be replaced with real ones, and on lock
 * it holds real secrets that must be dropped. Skipping the lock side would leave
 * plaintext sitting in the query cache after the window closed, which is the
 * hole the gate exists to close (agent-os-7o5s).
 *
 * A query nobody is watching is REMOVED, not invalidated (agent-os-4xsq):
 * invalidating only marks it stale, and an inactive query keeps its data until
 * gcTime, so a stack opened inside that window rendered the cached plaintext. A
 * mounted query is invalidated instead, which refetches it. Removing it would
 * leave its editor with no previous data, and the editor keeps unsaved edits
 * across a lock by reading its last payload (agent-os-vqf5).
 *
 * The stack-env keys are matched by predicate rather than by id because any
 * number of stacks may have been visited, and each one cached its own env query.
 */
export function invalidateEnvUnlockQueries(queryClient: QueryClient): Promise<void> {
  const isEnvQuery = (query: Query) =>
    (query.queryKey[0] === 'stack' && query.queryKey[2] === 'env') ||
    partialMatchKey(query.queryKey, queryKeys.settings.globalEnv())

  queryClient.removeQueries({
    predicate: (query) => isEnvQuery(query) && query.getObserversCount() === 0,
  })
  return queryClient.invalidateQueries({ predicate: isEnvQuery }).then(() => undefined)
}
