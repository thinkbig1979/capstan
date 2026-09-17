import { useMutation, useQueryClient, type UseMutationResult, type QueryKey } from '@tanstack/react-query'
import { toast } from 'sonner'
import { isActionResult, toastForResult, type ActionResult } from '@/lib/action-result'
import { classifyError } from '@/lib/error-handler'

export interface UseActionMutationOptions<TVars, TData extends ActionResult> {
  mutationFn: (vars: TVars) => Promise<TData>
  /** Query keys to invalidate after a successful mutation. */
  invalidate?: QueryKey[]
  /** Override the success toast title (defaults to result.reason). */
  successTitle?: string
  /** Called after toastForResult and invalidations on success. */
  onResult?: (r: TData) => void
}

/**
 * Thin, typed useMutation wrapper that enforces the Action Truth Contract on
 * every mutation:
 *  - onSuccess: fires toastForResult (derives toast level from outcome), then
 *    invalidates all provided query keys, then calls onResult.
 *  - onError: renders the ActionResult's own reason when the rejection carries
 *    one, and otherwise classifies the error via classifyError.
 *
 * Replaces ad-hoc `onSuccess: toast.success(...)` (audit finding P-6).
 */
export function useActionMutation<TVars, TData extends ActionResult = ActionResult>(
  opts: UseActionMutationOptions<TVars, TData>,
): UseMutationResult<TData, unknown, TVars> {
  const queryClient = useQueryClient()

  return useMutation<TData, unknown, TVars>({
    mutationFn: opts.mutationFn,
    onSuccess: (data) => {
      toastForResult(data, { successTitle: opts.successTitle })
      for (const key of opts.invalidate ?? []) {
        queryClient.invalidateQueries({ queryKey: key })
      }
      opts.onResult?.(data)
    },
    onError: (err) => {
      // A FAILED action answers 5xx, so axios rejects and api.ts's interceptor
      // hands us {...body, status} — the ActionResult itself. classifyError
      // cannot read it: it looks for data.error / data.message / err.message
      // and an ActionResult carries none of them, so the cause fell through to
      // the 5xx branch and was replaced by a bare status string (agent-os-ug4t).
      // Worst case was the Docker outage, whose reason IS the recovery.
      //
      // `err.reason` is checked, not just the type: toastForResult's failed arm
      // is toast.error(r.reason) with no fallback, so an empty reason would
      // render an empty toast — worse than the generic sentence.
      if (isActionResult(err) && err.reason) {
        toastForResult(err)
        return
      }
      toast.error(classifyError(err).message)
    },
  })
}
