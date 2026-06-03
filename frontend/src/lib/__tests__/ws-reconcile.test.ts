import { describe, it, expect, vi } from 'vitest'
import { reconcileOnClose } from '../ws-reconcile'

describe('reconcileOnClose', () => {
  it('calls refetch when completed is false', () => {
    const refetch = vi.fn()
    reconcileOnClose({ completed: false, refetch })
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('does NOT call refetch when completed is true', () => {
    const refetch = vi.fn()
    reconcileOnClose({ completed: true, refetch })
    expect(refetch).not.toHaveBeenCalled()
  })

  it('calls refetch each time for repeated incomplete closes', () => {
    const refetch = vi.fn()
    reconcileOnClose({ completed: false, refetch })
    reconcileOnClose({ completed: false, refetch })
    expect(refetch).toHaveBeenCalledTimes(2)
  })
})
