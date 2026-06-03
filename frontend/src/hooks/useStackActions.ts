import { useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi, type LifecycleResult, type StackDeleteResult } from '@/lib/api'
import { toast } from 'sonner'
import { toastForResult, isActionResult } from '@/lib/action-result'
import { classifyError } from '@/lib/error-handler'

const INVALIDATE_KEYS = [
  ['stacks'],
  ['stack'],
  ['dashboard-stats'],
] as const

type StackAction = 'start' | 'stop' | 'restart' | 'delete'

/**
 * Success title passed to toastForResult as the toast heading.
 * The reason from the ActionResult body becomes the description.
 */
const ACTION_SUCCESS_TITLES: Record<StackAction, string> = {
  start: 'Stack started',
  stop: 'Stack stopped',
  restart: 'Stack restarted',
  delete: 'Stack deleted',
}

interface UseStackActionsOptions {
  onSuccess?: (action: StackAction, id: string) => void
  onError?: (action: StackAction, id: string) => void
  /** Called after toastForResult when the backend returns a typed ActionResult. */
  onResult?: (action: StackAction, id: string) => void
}

/** Union of all possible return types from the lifecycle + delete mutations. */
type AnyLifecycleResult = LifecycleResult | StackDeleteResult

/**
 * Extract the best error message from a rejected mutation value.
 *
 * The axios interceptor in api.ts rejects with `error.response?.data` directly
 * (the parsed response body), so for a 500 ActionResult the rejected value IS
 * {outcome:'failed', reason:'...'} — no unwrapping needed.
 *
 * Priority:
 *  1. ActionResult body with a reason  → use reason (server-authored, specific)
 *  2. Anything else                    → classifyError for a human-readable fallback
 */
function errorMessage(action: StackAction, err: unknown): string {
  if (isActionResult(err)) {
    return err.reason || `Failed to ${action} stack`
  }
  return classifyError(err).message || `Failed to ${action} stack`
}

export function useStackActions(options?: UseStackActionsOptions) {
  const queryClient = useQueryClient()

  function invalidateAll() {
    for (const key of INVALIDATE_KEYS) {
      queryClient.invalidateQueries({ queryKey: [...key] })
    }
  }

  function createMutation(action: StackAction) {
    return useMutation<AnyLifecycleResult, unknown, string>({
      mutationFn: (id: string): Promise<AnyLifecycleResult> => {
        if (action === 'delete') return stacksApi.delete(id)
        return stacksApi[action](id)
      },
      onSuccess: (data, id) => {
        // All four actions (start/stop/restart/delete) return a typed
        // ActionResult body: derive the toast level from outcome.
        // success→toast.success, no_change→toast.info,
        // partial→toast.warning, failed→toast.error.
        // A crash-loop or no-op start will NEVER show as green success.
        if (isActionResult(data)) {
          toastForResult(data, { successTitle: ACTION_SUCCESS_TITLES[action] })
        }
        invalidateAll()
        options?.onSuccess?.(action, id)
        if (isActionResult(data)) {
          options?.onResult?.(action, id)
        }
      },
      onError: (err, id) => {
        // A 500 `failed` ActionResult body is the rejected value directly
        // (the axios interceptor strips the AxiosError wrapper). Surface the
        // server-authored reason when available so the user sees a specific
        // message rather than the generic fallback.
        toast.error(errorMessage(action, err))
        options?.onError?.(action, id)
      },
    })
  }

  const start = createMutation('start')
  const stop = createMutation('stop')
  const restart = createMutation('restart')
  const deleteAction = createMutation('delete')

  return { start, stop, restart, delete: deleteAction }
}

export type { StackAction }
