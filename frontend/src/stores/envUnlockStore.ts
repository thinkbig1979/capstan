import { create } from 'zustand'
import { toast } from 'sonner'

export const UNLOCK_DURATION_MS = 5 * 60 * 1000
const WARNING_BEFORE_EXPIRY_MS = 15 * 1000

interface EnvUnlockState {
  unlockedUntil: number | null
  /**
   * The unlock token minted by POST /auth/verify-password. api.ts sends it as
   * X-Unlock-Token, and the backend withholds secret values from any request
   * that arrives without a live one — so this is no longer a client-side
   * courtesy, it is the credential (agent-os-7o5s).
   */
  token: string | null
  _expiryTimer: ReturnType<typeof setTimeout> | null
  _warningTimer: ReturnType<typeof setTimeout> | null
  unlock: (token?: string | null) => void
  lock: () => void
  isUnlocked: () => boolean
  msRemaining: () => number
}

function clearTimers(state: { _expiryTimer: ReturnType<typeof setTimeout> | null; _warningTimer: ReturnType<typeof setTimeout> | null }) {
  if (state._expiryTimer) clearTimeout(state._expiryTimer)
  if (state._warningTimer) clearTimeout(state._warningTimer)
}

export const useEnvUnlockStore = create<EnvUnlockState>()((set, get) => ({
  unlockedUntil: null,
  token: null,
  _expiryTimer: null,
  _warningTimer: null,

  unlock: (token = null) => {
    const current = get()
    clearTimers(current)

    const until = Date.now() + UNLOCK_DURATION_MS

    const warningTimer = setTimeout(() => {
      toast.warning('Session expiring in 15 seconds')
    }, UNLOCK_DURATION_MS - WARNING_BEFORE_EXPIRY_MS)

    const expiryTimer = setTimeout(() => {
      // Drop the token with the window. Keeping it would leave the client
      // sending a credential it believes has expired, and the server would
      // honour it until its own TTL ran out.
      set({ unlockedUntil: null, token: null, _expiryTimer: null, _warningTimer: null })
      toast.info('Environment variables locked')
    }, UNLOCK_DURATION_MS)

    set({
      unlockedUntil: until,
      token,
      _expiryTimer: expiryTimer,
      _warningTimer: warningTimer,
    })
  },

  lock: () => {
    const current = get()
    clearTimers(current)
    set({ unlockedUntil: null, token: null, _expiryTimer: null, _warningTimer: null })
  },

  isUnlocked: () => {
    const until = get().unlockedUntil
    return until !== null && until > Date.now()
  },

  msRemaining: () => {
    const until = get().unlockedUntil
    if (until === null) return 0
    return Math.max(0, until - Date.now())
  },
}))

/**
 * Reads the current unlock token from outside React. Used by the api.ts request
 * interceptor, which runs on plain axios calls with no hook context available.
 */
export function currentUnlockToken(): string | null {
  return useEnvUnlockStore.getState().token
}
