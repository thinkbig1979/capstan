import type { QueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/query-keys'

/**
 * Refetches every query the backend redacts without an unlock token.
 *
 * Called on both edges of the unlock window, for opposite reasons: on unlock the
 * cache holds blanked secrets that must be replaced with real ones, and on lock
 * it holds real secrets that must be dropped. Skipping the lock side would leave
 * plaintext sitting in the query cache after the window closed, which is the
 * hole the gate exists to close (agent-os-7o5s).
 *
 * The stack-env keys are matched by predicate rather than by id because any
 * number of stacks may have been visited, and each one cached its own env query.
 */
export function invalidateEnvUnlockQueries(queryClient: QueryClient): Promise<void> {
  return Promise.all([
    queryClient.invalidateQueries({
      predicate: (query) => query.queryKey[0] === 'stack' && query.queryKey[2] === 'env',
    }),
    queryClient.invalidateQueries({ queryKey: queryKeys.settings.globalEnv() }),
  ]).then(() => undefined)
}
