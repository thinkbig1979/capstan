import { describe, it, expect } from 'vitest'
import { isFirstLoad } from '../query-state'

describe('isFirstLoad (agent-os-7nqa)', () => {
  it.each([
    ['fetching, no data', { isPending: true, fetchStatus: 'fetching' as const }, true],
    ['paused (offline), no data', { isPending: true, fetchStatus: 'paused' as const }, true],
    ['disabled, no data', { isPending: true, fetchStatus: 'idle' as const }, false],
    ['has data, refetching', { isPending: false, fetchStatus: 'fetching' as const }, false],
    ['has data, refetch paused', { isPending: false, fetchStatus: 'paused' as const }, false],
    ['has data, idle', { isPending: false, fetchStatus: 'idle' as const }, false],
  ])('%s', (_name, state, expected) => {
    expect(isFirstLoad(state)).toBe(expected)
  })
})
