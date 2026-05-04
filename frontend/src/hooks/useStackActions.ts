import { useMutation, useQueryClient } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { toast } from 'sonner'

const INVALIDATE_KEYS = [
  ['stacks'],
  ['stack'],
  ['dashboard-stats'],
] as const

type StackAction = 'start' | 'stop' | 'restart' | 'delete'

const SUCCESS_MESSAGES: Record<StackAction, string> = {
  start: 'Stack started successfully',
  stop: 'Stack stopped successfully',
  restart: 'Stack restarted successfully',
  delete: 'Stack deleted successfully',
}

const ERROR_MESSAGES: Record<StackAction, string> = {
  start: 'Failed to start stack',
  stop: 'Failed to stop stack',
  restart: 'Failed to restart stack',
  delete: 'Failed to delete stack',
}

const ACTION_FNS: Record<StackAction, (id: string) => Promise<unknown>> = {
  start: stacksApi.start,
  stop: stacksApi.stop,
  restart: stacksApi.restart,
  delete: stacksApi.delete,
}

interface UseStackActionsOptions {
  onSuccess?: (action: StackAction, id: string) => void
  onError?: (action: StackAction, id: string) => void
}

export function useStackActions(options?: UseStackActionsOptions) {
  const queryClient = useQueryClient()

  function createMutation(action: StackAction) {
    return useMutation({
      mutationFn: ACTION_FNS[action],
      onSuccess: (_data, id) => {
        toast.success(SUCCESS_MESSAGES[action])
        for (const key of INVALIDATE_KEYS) {
          queryClient.invalidateQueries({ queryKey: [...key] })
        }
        options?.onSuccess?.(action, id)
      },
      onError: (_error, id) => {
        toast.error(ERROR_MESSAGES[action])
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
