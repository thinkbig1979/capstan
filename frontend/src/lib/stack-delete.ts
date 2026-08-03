import { stacksApi, type StackDeleteResult } from '@/lib/api'

const COLLATERAL_CODE = 'STACK_DELETE_COLLATERAL'

/**
 * Signature of useConfirm().confirm — kept structural (not a direct import of
 * the hook's type) so this module stays a plain, hook-free function callable
 * from any mutationFn.
 */
export type ConfirmFn = (
  title: string,
  description: string,
  options?: { confirmText?: string; isDangerous?: boolean },
) => Promise<boolean>

/**
 * Thrown when the user declines the second (collateral) confirmation. This is
 * a user cancel, not a failure — callers must not surface it as a generic
 * error toast, just reset any "deleting" UI state.
 */
export class StackDeleteCancelledError extends Error {
  constructor() {
    super('Stack delete cancelled by user after collateral confirmation')
    this.name = 'StackDeleteCancelledError'
  }
}

/**
 * The backend AppError body (models/errors.go) for a refused delete:
 *   { code: 'STACK_DELETE_COLLATERAL', message, details: { directory, collateral } }
 * api.ts's axios interceptor rejects with this body directly (unwrapped from
 * the AxiosError), so that is exactly the shape callers see here.
 */
function collateralDetails(err: unknown): { directory: string; collateral: string[] } | null {
  const body = err as { code?: string; details?: { directory?: string; collateral?: string[] } } | null | undefined
  if (!body || body.code !== COLLATERAL_CODE) return null
  return {
    directory: body.details?.directory ?? '',
    collateral: body.details?.collateral ?? [],
  }
}

/**
 * Deletes a stack, re-confirming with the caller-supplied `confirm` when the
 * backend refuses with 428 STACK_DELETE_COLLATERAL — i.e. the stack's
 * directory holds more than its own compose/env file (a bind-mounted data
 * dir, .git, operator notes; see agent-os-lg2). The retry only ever happens
 * after the user explicitly confirms the enumerated list; declining throws
 * StackDeleteCancelledError and never re-issues the delete.
 */
export async function deleteStackWithCollateralConfirm(
  id: string,
  confirm: ConfirmFn,
): Promise<StackDeleteResult> {
  try {
    return await stacksApi.delete(id)
  } catch (err) {
    const collateral = collateralDetails(err)
    if (!collateral) throw err

    const list = collateral.collateral.length > 0 ? collateral.collateral.join(', ') : 'other files'
    const directory = collateral.directory || "this stack's directory"
    const proceed = await confirm(
      'Also delete these files?',
      `${directory} contains more than the stack itself: ${list}. Deleting the stack will permanently remove these too. This cannot be undone.`,
      { confirmText: 'Delete Everything', isDangerous: true },
    )
    if (!proceed) throw new StackDeleteCancelledError()

    return await stacksApi.delete(id, true)
  }
}
