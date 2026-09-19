import { toast } from 'sonner'

import type { Outcome } from '@/types'

type ActionOutcome = 'success' | 'no_change' | 'partial' | 'failed'

/**
 * ActionOutcome is spelled out rather than aliased to Outcome so the switch in
 * toastForResult stays exhaustive and readable at the point of use. The cost of
 * spelling it out is that it can drift from the Go constants it mirrors, so the
 * two unions are proved equal at compile time instead.
 *
 * Exact is bidirectional on purpose: a one-way `extends` would stay green if the
 * backend GREW a new outcome, which is exactly the case that must fail — a new
 * Go constant reaching a switch that has no arm for it is the bug this guards.
 * Rename or add a truth.Outcome constant and this line is a TS2344 naming both
 * unions, not a silent widening discovered in a browser.
 */
type Exact<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false
type AssertTrue<T extends true> = T
export type ActionOutcomeMatchesWire = AssertTrue<Exact<ActionOutcome, Outcome>>

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
