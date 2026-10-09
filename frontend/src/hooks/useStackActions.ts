import { stacksApi, type LifecycleResult, type StackDeleteResult } from '@/lib/api'
import { useActionMutation } from '@/hooks/useActionMutation'
import { queryKeys } from '@/lib/query-keys'
import { deleteStackWithCollateralConfirm, StackDeleteCancelledError, type ConfirmFn } from '@/lib/stack-delete'

const INVALIDATE_KEYS = [
  queryKeys.stacks(),
  // Broad prefix: reaches every ['stack', id, …] entry, not one stack.
  queryKeys.stack.all(),
  queryKeys.dashboardStats(),
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
  /**
   * Re-confirmation surface for a delete the backend refuses with 428
   * STACK_DELETE_COLLATERAL (see deleteStackWithCollateralConfirm). Pass the
   * `confirm` from useConfirm(). Without it, delete falls back to the plain
   * single-confirmation call and a 428 surfaces as a normal error toast.
   */
  confirmCollateral?: ConfirmFn
}

/** Union of all possible return types from the lifecycle + delete mutations. */
type AnyLifecycleResult = LifecycleResult | StackDeleteResult

/**
 * A single lifecycle-action mutation. Extracted as its own custom hook (rather
 * than a plain factory function that calls `useActionMutation` internally) so
 * each of the four mutations below is a direct, unconditional hook call at the
 * top of `useStackActions` — the call order is a fixed sequence of statements,
 * not indirection through a helper, so React can verify hook-call safety.
 *
 * All four actions (start/stop/restart/delete) return a typed ActionResult
 * body, so useActionMutation derives the toast level from the outcome
 * (success→success, no_change→info, partial→warning, failed→error): a
 * crash-loop or no-op start NEVER shows as green success. A rejected `failed`
 * result (a 5xx ActionResult body, which api.ts's interceptor hands over as the
 * rejection itself) is titled with the action, the server-authored reason as
 * its description; a rejected partial keeps its warning level.
 */
function useStackActionMutation(action: StackAction, options?: UseStackActionsOptions) {
  return useActionMutation<string, AnyLifecycleResult>({
    mutationFn: (id: string): Promise<AnyLifecycleResult> => {
      if (action === 'delete') {
        return options?.confirmCollateral
          ? deleteStackWithCollateralConfirm(id, options.confirmCollateral)
          : stacksApi.delete(id)
      }
      return stacksApi[action](id)
    },
    successTitle: ACTION_SUCCESS_TITLES[action],
    errorTitle: `Failed to ${action} stack`,
    // Broad prefix in INVALIDATE_KEYS reaches every ['stack', id, …] entry.
    invalidate: INVALIDATE_KEYS.map((key) => [...key]),
    // A declined collateral confirmation is a user cancel, not a failure: no
    // error toast, but the caller's onError still fires (wrapper contract) so
    // it can reset local "deleting" state (see DashboardPage/StackPage).
    silentWhen: (err) => err instanceof StackDeleteCancelledError,
    onResult: (_result, id) => {
      options?.onSuccess?.(action, id)
      options?.onResult?.(action, id)
    },
    onError: (_err, id) => options?.onError?.(action, id),
  })
}

export function useStackActions(options?: UseStackActionsOptions) {
  const start = useStackActionMutation('start', options)
  const stop = useStackActionMutation('stop', options)
  const restart = useStackActionMutation('restart', options)
  const deleteAction = useStackActionMutation('delete', options)

  return { start, stop, restart, delete: deleteAction }
}

export type { StackAction }
