/**
 * agent-os-k564 review F4: useStillHoldsSent compares what a save sent against
 * the LATEST editor state, read through a ref, not the state of the render that
 * clicked Save. In the editors a refetch hides a frozen ref (an equal payload
 * hydrates the editor clean anyway), so the hook is pinned on its own.
 */
import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useStillHoldsSent } from '../useStillHoldsSent'

const same = (latest: string, sent: string) => latest === sent

describe('useStillHoldsSent', () => {
  it('nothing remembered: false', () => {
    const { result } = renderHook(({ v }) => useStillHoldsSent(v, same), { initialProps: { v: 'a' } })
    expect(result.current.stillHolds()).toBe(false)
  })

  it('the editor still holds what was sent: true', () => {
    const { result, rerender } = renderHook(({ v }) => useStillHoldsSent(v, same), { initialProps: { v: 'a' } })
    result.current.remember('a')
    rerender({ v: 'a' })
    expect(result.current.stillHolds()).toBe(true)
  })

  it('the editor moved on after the save: false, read through a callback from the earlier render', () => {
    const { result, rerender } = renderHook(({ v }) => useStillHoldsSent(v, same), { initialProps: { v: 'a' } })
    const atClick = result.current
    atClick.remember('a')
    rerender({ v: 'ab' })
    expect(atClick.stillHolds()).toBe(false)
  })

  it('typed away and back: true', () => {
    const { result, rerender } = renderHook(({ v }) => useStillHoldsSent(v, same), { initialProps: { v: 'a' } })
    const atClick = result.current
    atClick.remember('a')
    rerender({ v: 'ab' })
    rerender({ v: 'a' })
    expect(atClick.stillHolds()).toBe(true)
  })
})
