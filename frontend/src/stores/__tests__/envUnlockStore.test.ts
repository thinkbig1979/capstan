import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useEnvUnlockStore, UNLOCK_DURATION_MS } from '../envUnlockStore'

vi.mock('sonner', () => ({
  toast: { warning: vi.fn(), info: vi.fn() },
}))

import { toast } from 'sonner'

const WARNING_LEAD_MS = 15 * 1000

beforeEach(() => {
  vi.clearAllMocks()
  useEnvUnlockStore.getState().lock()
})

afterEach(() => {
  useEnvUnlockStore.getState().lock()
  vi.useRealTimers()
})

describe('envUnlockStore initial state', () => {
  it('starts locked', () => {
    expect(useEnvUnlockStore.getState().unlockedUntil).toBeNull()
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(false)
    expect(useEnvUnlockStore.getState().msRemaining()).toBe(0)
  })
})

describe('unlock()', () => {
  it('sets unlockedUntil roughly UNLOCK_DURATION_MS in the future and reports unlocked', () => {
    const before = Date.now()
    useEnvUnlockStore.getState().unlock()
    const after = Date.now()

    const until = useEnvUnlockStore.getState().unlockedUntil
    expect(until).not.toBeNull()
    expect(until as number).toBeGreaterThanOrEqual(before + UNLOCK_DURATION_MS)
    expect(until as number).toBeLessThanOrEqual(after + UNLOCK_DURATION_MS)
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)
    expect(useEnvUnlockStore.getState().msRemaining()).toBeGreaterThan(0)
    expect(useEnvUnlockStore.getState().msRemaining()).toBeLessThanOrEqual(UNLOCK_DURATION_MS)
  })

  it('re-unlocking cancels the previous session timers instead of stacking them', () => {
    vi.useFakeTimers()
    useEnvUnlockStore.getState().unlock()

    // Advance partway through the first session, then unlock again.
    vi.advanceTimersByTime(UNLOCK_DURATION_MS / 2)
    useEnvUnlockStore.getState().unlock()

    // If the first session's expiry timer had not been cleared, it would have
    // fired here (at UNLOCK_DURATION_MS / 2 + UNLOCK_DURATION_MS / 2 + 1 from
    // the original unlock) and forced unlockedUntil back to null prematurely.
    vi.advanceTimersByTime(UNLOCK_DURATION_MS / 2 + 1)
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)
    expect(toast.info).not.toHaveBeenCalled()
  })
})

describe('lock()', () => {
  it('immediately clears the unlock session', () => {
    useEnvUnlockStore.getState().unlock()
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)

    useEnvUnlockStore.getState().lock()

    expect(useEnvUnlockStore.getState().unlockedUntil).toBeNull()
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(false)
    expect(useEnvUnlockStore.getState().msRemaining()).toBe(0)
  })

  it('cancels the pending expiry/warning timers so they never fire after a manual lock', () => {
    vi.useFakeTimers()
    useEnvUnlockStore.getState().unlock()
    useEnvUnlockStore.getState().lock()

    // Advance well past both the warning and expiry deadlines.
    vi.advanceTimersByTime(UNLOCK_DURATION_MS + 1000)

    expect(toast.warning).not.toHaveBeenCalled()
    expect(toast.info).not.toHaveBeenCalled()
  })
})

describe('auto-expiry', () => {
  it('re-locks itself and toasts once UNLOCK_DURATION_MS elapses without a manual lock', () => {
    vi.useFakeTimers()
    useEnvUnlockStore.getState().unlock()
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)

    vi.advanceTimersByTime(UNLOCK_DURATION_MS)

    expect(useEnvUnlockStore.getState().unlockedUntil).toBeNull()
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(false)
    expect(toast.info).toHaveBeenCalledWith('Environment variables locked')
  })

  it('fires the 15s warning toast before expiry while still unlocked', () => {
    vi.useFakeTimers()
    useEnvUnlockStore.getState().unlock()

    vi.advanceTimersByTime(UNLOCK_DURATION_MS - WARNING_LEAD_MS)

    expect(toast.warning).toHaveBeenCalledWith('Session expiring in 15 seconds')
    // Still unlocked — the warning fires before the session actually ends.
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)
    expect(toast.info).not.toHaveBeenCalled()
  })
})

describe('persistence', () => {
  it('does not read or write any localStorage key (in-memory session only)', () => {
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem')
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem')

    useEnvUnlockStore.getState().unlock()
    useEnvUnlockStore.getState().lock()

    expect(setItemSpy).not.toHaveBeenCalled()
    expect(getItemSpy).not.toHaveBeenCalled()

    setItemSpy.mockRestore()
    getItemSpy.mockRestore()
  })
})
