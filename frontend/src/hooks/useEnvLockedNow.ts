import { useAuthStore } from '@/stores/authStore'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

/**
 * True while env values are locked for this session right now: auth is on and
 * no unlock window is open. Every display gate over a query the backend
 * redacts reads this as well as the payload's own `locked` flag. After a lock
 * the last payload is still the one served unlocked, secrets in the clear,
 * until the lock's refetch lands, and for good if it fails (agent-os-wm7t,
 * xz6h, uzgo).
 */
export function useEnvLockedNow(): boolean {
  const authDisabled = useAuthStore((s) => s.authDisabled)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)
  return !authDisabled && unlockedUntil === null
}
