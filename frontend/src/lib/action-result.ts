import { toast } from 'sonner'

export type ActionOutcome = 'success' | 'no_change' | 'partial' | 'failed'

export interface ActionResult<D = Record<string, unknown>> {
  outcome: ActionOutcome
  reason: string
  details?: D
}

/**
 * Fires the appropriate sonner toast for a typed ActionResult.
 * - success   → toast.success
 * - no_change → toast.info  (already up to date)
 * - partial   → toast.warning
 * - failed    → toast.error
 */
export function toastForResult(
  r: ActionResult,
  opts?: { successTitle?: string },
): void {
  switch (r.outcome) {
    case 'success':
      toast.success(opts?.successTitle ?? r.reason)
      break
    case 'no_change':
      toast.info(r.reason || 'Already up to date')
      break
    case 'partial':
      toast.warning(r.reason)
      break
    case 'failed':
      toast.error(r.reason)
      break
  }
}

/**
 * Type-guard that narrows an unknown value to ActionResult.
 * Checks only the discriminant field (outcome) so callers can handle
 * both legacy and new responses safely during migration.
 */
export function isActionResult(x: unknown): x is ActionResult {
  if (typeof x !== 'object' || x === null) return false
  const outcome = (x as Record<string, unknown>).outcome
  return (
    outcome === 'success' ||
    outcome === 'no_change' ||
    outcome === 'partial' ||
    outcome === 'failed'
  )
}
