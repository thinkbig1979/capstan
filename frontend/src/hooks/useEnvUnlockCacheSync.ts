import { useEffect, useRef } from 'react'
import { queryClient } from '@/lib/query-client'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

/**
 * Drops the plaintext the backend handed us once the unlock window closes.
 *
 * The unlock token is what makes the secret endpoints answer in full, so while
 * it is live the query cache holds real secret values. When the window ends —
 * manual Lock, or the 5-minute auto-expiry — the components re-mask what they
 * render, but the cache would still hold the plaintext until something else
 * invalidated it. Refetching here replaces it with the redacted payload.
 *
 * Mounted once, at the app root: the stack .env editor and the global-env
 * settings panel each cache their own query, and either may be mounted when the
 * window closes.
 */
export function useEnvUnlockCacheSync() {
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)
  // unlockedUntil starts null, so without this the very first render would look
  // like a lock transition and refetch on boot for no reason.
  const wasUnlocked = useRef(false)

  useEffect(() => {
    if (unlockedUntil !== null) {
      wasUnlocked.current = true
      return
    }
    if (!wasUnlocked.current) return
    wasUnlocked.current = false
    void invalidateEnvUnlockQueries(queryClient)
  }, [unlockedUntil])
}
