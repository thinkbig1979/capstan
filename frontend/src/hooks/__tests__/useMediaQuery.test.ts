import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useMediaQuery } from '../useMediaQuery'

// Minimal controllable MediaQueryList: flip `matches` then fire a change event.
function installMatchMedia(initialMatches: boolean) {
  let matches = initialMatches
  const listeners = new Set<() => void>()

  const mql = {
    get matches() {
      return matches
    },
    addEventListener: (_: string, cb: () => void) => listeners.add(cb),
    removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
  }

  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => mql),
  )

  return {
    emit(next: boolean) {
      matches = next
      listeners.forEach((cb) => cb())
    },
    listenerCount: () => listeners.size,
  }
}

describe('useMediaQuery', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns the initial match state', () => {
    installMatchMedia(true)
    const { result } = renderHook(() => useMediaQuery('(min-width: 768px)'))
    expect(result.current).toBe(true)
  })

  it('re-renders when the query starts/stops matching', () => {
    const mm = installMatchMedia(false)
    const { result } = renderHook(() => useMediaQuery('(min-width: 768px)'))
    expect(result.current).toBe(false)

    act(() => mm.emit(true))
    expect(result.current).toBe(true)

    act(() => mm.emit(false))
    expect(result.current).toBe(false)
  })

  it('unsubscribes on unmount', () => {
    const mm = installMatchMedia(false)
    const { unmount } = renderHook(() => useMediaQuery('(min-width: 768px)'))
    expect(mm.listenerCount()).toBeGreaterThan(0)
    unmount()
    expect(mm.listenerCount()).toBe(0)
  })
})
