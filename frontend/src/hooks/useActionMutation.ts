import { useMutation, useQueryClient, type UseMutationResult, type QueryKey } from '@tanstack/react-query'
import { toast } from 'sonner'
import { toastForResult, type ActionResult } from '@/lib/action-result'
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
 *  - onError: classifies the error via classifyError and fires toast.error.
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
      toast.error(classifyError(err).message)
    },
  })
}
