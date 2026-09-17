import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockDelete = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: { delete: (...args: unknown[]) => mockDelete(...args) },
}))

import { deleteStackWithCollateralConfirm } from '../stack-delete'

beforeEach(() => {
  vi.clearAllMocks()
})

/**
 * agent-os-06c1. collateralDetails declares
 *   { directory: string; collateral: string[] }
 * and reaches it through `err as { details?: { directory?: string; collateral?: string[] } }`
 * on a value that is `unknown`. Both fields are rendered into the confirm
 * dialog a user reads before agreeing to a destructive delete, and
 * `collateral.join(', ')` throws outright when the field is not an array.
 */
describe('deleteStackWithCollateralConfirm — the 428 details are asserted, not validated', () => {
  const refusal = (details: unknown) => ({ code: 'STACK_DELETE_COLLATERAL', details })

  it('does not render a non-string directory into the confirmation a user reads', async () => {
    mockDelete.mockRejectedValueOnce(refusal({ directory: { path: '/srv/app' }, collateral: [] }))
    const confirm = vi.fn().mockResolvedValue(false)

    await expect(deleteStackWithCollateralConfirm('app', confirm)).rejects.toThrow()

    const description = confirm.mock.calls[0][1] as string
    expect(description).not.toContain('[object Object]')
  })

  it('does not throw when collateral is not an array', async () => {
    mockDelete.mockRejectedValueOnce(refusal({ directory: '/srv/app', collateral: 'compose.yaml' }))
    const confirm = vi.fn().mockResolvedValue(false)

    // Rejects with the user-cancel error, NOT with a TypeError from .join.
    await expect(deleteStackWithCollateralConfirm('app', confirm)).rejects.toThrow(
      'Stack delete cancelled by user',
    )
    expect(confirm).toHaveBeenCalled()
  })

  it('does not render non-string collateral members into the list', async () => {
    mockDelete.mockRejectedValueOnce(
      refusal({ directory: '/srv/app', collateral: ['.git', { name: 'data' }] }),
    )
    const confirm = vi.fn().mockResolvedValue(false)

    await expect(deleteStackWithCollateralConfirm('app', confirm)).rejects.toThrow()
    expect(confirm.mock.calls[0][1] as string).not.toContain('[object Object]')
  })

  // The must-be-green control: a well-formed refusal is unchanged by the
  // narrowing, which is agent-os-06c1 criterion 5 at this site.
  it('still renders a well-formed refusal exactly as before', async () => {
    mockDelete
      .mockRejectedValueOnce(refusal({ directory: '/srv/app', collateral: ['.git', 'data/'] }))
      .mockResolvedValueOnce({ outcome: 'success', reason: 'Stack deleted' })
    const confirm = vi.fn().mockResolvedValue(true)

    await deleteStackWithCollateralConfirm('app', confirm)

    expect(confirm.mock.calls[0][1]).toBe(
      '/srv/app contains more than the stack itself: .git, data/. Deleting the stack will ' +
        'permanently remove these too. This cannot be undone.',
    )
    expect(mockDelete).toHaveBeenNthCalledWith(2, 'app', true)
  })

  it('passes a non-collateral rejection straight through', async () => {
    const other = { code: 'NOT_FOUND', message: 'no such stack' }
    mockDelete.mockRejectedValueOnce(other)
    const confirm = vi.fn()

    await expect(deleteStackWithCollateralConfirm('app', confirm)).rejects.toBe(other)
    expect(confirm).not.toHaveBeenCalled()
  })
})
