import { create } from 'zustand'
import { toast } from 'sonner'

export const UNLOCK_DURATION_MS = 5 * 60 * 1000
const WARNING_BEFORE_EXPIRY_MS = 15 * 1000

interface EnvUnlockState {
  unlockedUntil: number | null
  _expiryTimer: ReturnType<typeof setTimeout> | null
  _warningTimer: ReturnType<typeof setTimeout> | null
  unlock: () => void
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
  _expiryTimer: null,
  _warningTimer: null,

  unlock: () => {
    const current = get()
    clearTimers(current)

    const until = Date.now() + UNLOCK_DURATION_MS

    const warningTimer = setTimeout(() => {
      toast.warning('Session expiring in 15 seconds')
    }, UNLOCK_DURATION_MS - WARNING_BEFORE_EXPIRY_MS)

    const expiryTimer = setTimeout(() => {
      set({ unlockedUntil: null, _expiryTimer: null, _warningTimer: null })
      toast.info('Environment variables locked')
    }, UNLOCK_DURATION_MS)

    set({
      unlockedUntil: until,
      _expiryTimer: expiryTimer,
      _warningTimer: warningTimer,
    })
  },

  lock: () => {
    const current = get()
    clearTimers(current)
    set({ unlockedUntil: null, _expiryTimer: null, _warningTimer: null })
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
