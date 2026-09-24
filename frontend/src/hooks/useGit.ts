import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { gitApi } from '@/lib/api'
import type { ActionResult } from '@/lib/action-result'
import { useActionMutation } from '@/hooks/useActionMutation'
import { queryKeys } from '@/lib/query-keys'
import { stringOr } from '@/lib/narrow'

export function useGitStatus(stackId: string) {
  return useQuery({
    queryKey: queryKeys.git.all(stackId),
    queryFn: () => gitApi.status(stackId),
    staleTime: 60000,
    // Neither of the two routine negative answers is an error any more, so
    // neither reaches this option: a non-git stack returns 200
    // `{isRepo: false}` (agent-os-x40a) and a repository with no commits yet
    // returns 200 `{isRepo: true, hasCommits: false}` (agent-os-4a4a). Both
    // render a chip or nothing, from DATA, and neither hides the panel.
    //
    // What still does reach it is definitive — STACK_DIR_MISSING (the directory
    // is gone) and NOT_FOUND (unknown stackId). Neither is a transient fault,
    // so retrying just repeats the error in the console/network tab. A failure
    // renders GitStatus's "status unknown" chip with Pull disabled
    // (agent-os-528x), and its Retry refetches on demand.
    retry: false,
  })
}

export function useGitLog(stackId: string, limit = 50, offset = 0, file?: string) {
  return useQuery({
    queryKey: queryKeys.git.log(stackId, limit, offset, file),
    queryFn: () => gitApi.log(stackId, limit, offset, file),
    staleTime: 60000,
  })
}

export function useGitDiff(stackId: string, hash: string) {
  return useQuery({
    queryKey: queryKeys.git.diff(stackId, hash),
    queryFn: () => gitApi.diff(stackId, hash),
    enabled: !!hash,
    staleTime: 60000,
  })
}

/**
 * Hook for git pull with proper Action Truth Contract handling.
 *
 * Accepts `stackId` and optional `redeploy` at mutation call time so callers
 * don't need to pass them to the hook itself.
 *
 * - success   → toast.success
 * - no_change → toast.info (already up to date)
 * - partial   → toast.warning with failed-redeploy list (audit finding #9)
 * - failed    → toast.error
 *
 * Always invalidates queryKeys.git.all(stackId) and queryKeys.stacks().
 */
export function useGitPull() {
  const queryClient = useQueryClient()

  return useActionMutation({
    mutationFn: async ({ stackId, redeploy = false }: { stackId: string; redeploy?: boolean }) => {
      const result = await gitApi.pull(stackId, redeploy)
      // Attach stackId so onResult can invalidate the per-stack git query
      return { ...result, _stackId: stackId }
    },
    invalidate: [queryKeys.stacks()],
    onResult: (result) => {
      const stackId = (result as ActionResult & { _stackId?: string })._stackId
      if (stackId) {
        queryClient.invalidateQueries({ queryKey: queryKeys.git.all(stackId) })
      }

      // On partial outcome, toastForResult already fired a warning.
      // Append the failed-redeploy details if available for more context.
      if (result.outcome === 'partial') {
        // agent-os-06c1: narrowed, not asserted. `?? []` only ever caught an
        // ABSENT list; a non-array threw on .map and a non-string member
        // rendered as [object Object] in the operator's warning.
        const rawFailed = (result.details as {
          failedRedeploys?: unknown
        } | undefined)?.failedRedeploys
        const failedRedeploys = Array.isArray(rawFailed) ? rawFailed : []
        if (failedRedeploys.length > 0) {
          const names = failedRedeploys
            .map((f) => stringOr((f as { stack?: unknown } | null)?.stack, 'unknown'))
            .join(', ')
          toast.warning(`Failed to redeploy: ${names}`)
        }
      }
    },
  })
}
