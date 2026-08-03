import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockDelete = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    delete: (...args: unknown[]) => mockDelete(...args),
  },
}))

import { deleteStackWithCollateralConfirm, StackDeleteCancelledError } from '@/lib/stack-delete'

beforeEach(() => {
  mockDelete.mockClear()
})

// The exact AppError body shape the backend renders for STACK_DELETE_COLLATERAL
// (models.AppError serializes {code, message, details} — Status is `json:"-"`,
// never sent). api.ts's axios interceptor rejects with this body directly (not
// wrapped in {response: {...}}), so that is exactly what
// deleteStackWithCollateralConfirm receives from a rejected stacksApi.delete().
function collateralError(directory = '/opt/stacks/my-stack', collateral = ['data', '.git', 'notes.md']) {
  return {
    code: 'STACK_DELETE_COLLATERAL',
    message: 'Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed',
    details: { directory, collateral },
  }
}

describe('deleteStackWithCollateralConfirm', () => {
  it('deletes without a second confirmation when the backend does not refuse', async () => {
    mockDelete.mockResolvedValueOnce({ outcome: 'success', reason: 'stack deleted' })
    const confirm = vi.fn()

    const result = await deleteStackWithCollateralConfirm('my-stack', confirm)

    expect(result).toEqual({ outcome: 'success', reason: 'stack deleted' })
    expect(mockDelete).toHaveBeenCalledTimes(1)
    expect(mockDelete).toHaveBeenCalledWith('my-stack')
    expect(confirm).not.toHaveBeenCalled()
  })

  it('re-confirms and retries with confirmCollateral=true after a 428, surfacing the collateral list', async () => {
    mockDelete
      .mockRejectedValueOnce(collateralError('/opt/stacks/my-stack', ['data', '.git', 'notes.md']))
      .mockResolvedValueOnce({ outcome: 'success', reason: 'stack deleted' })
    const confirm = vi.fn().mockResolvedValue(true)

    const result = await deleteStackWithCollateralConfirm('my-stack', confirm)

    expect(result).toEqual({ outcome: 'success', reason: 'stack deleted' })

    // The user-visible confirm must surface exactly the enumerated collateral.
    expect(confirm).toHaveBeenCalledTimes(1)
    const [, description] = confirm.mock.calls[0]
    expect(description).toContain('data')
    expect(description).toContain('.git')
    expect(description).toContain('notes.md')
    expect(description).toContain('/opt/stacks/my-stack')

    // The retry carries BOTH confirm=true (baked into stacksApi.delete itself)
    // and confirmCollateral=true.
    expect(mockDelete).toHaveBeenCalledTimes(2)
    expect(mockDelete).toHaveBeenNthCalledWith(1, 'my-stack')
    expect(mockDelete).toHaveBeenNthCalledWith(2, 'my-stack', true)
  })

  it('does NOT retry when the user declines the collateral confirmation', async () => {
    mockDelete.mockRejectedValueOnce(collateralError())
    const confirm = vi.fn().mockResolvedValue(false)

    await expect(deleteStackWithCollateralConfirm('my-stack', confirm)).rejects.toBeInstanceOf(StackDeleteCancelledError)

    // Exactly the first attempt — a decline must never trigger the retry.
    expect(mockDelete).toHaveBeenCalledTimes(1)
  })

  it('rethrows non-428 errors without prompting a second confirmation', async () => {
    mockDelete.mockRejectedValueOnce({ code: 'STACK_NOT_FOUND', message: 'not found' })
    const confirm = vi.fn()

    await expect(deleteStackWithCollateralConfirm('my-stack', confirm)).rejects.toEqual({
      code: 'STACK_NOT_FOUND',
      message: 'not found',
    })
    expect(confirm).not.toHaveBeenCalled()
    expect(mockDelete).toHaveBeenCalledTimes(1)
  })
})
