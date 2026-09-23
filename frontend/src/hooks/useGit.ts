import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { gitApi, type GitPullResult } from '@/lib/api'
import { isActionResult, type ActionResult } from '@/lib/action-result'
import { useActionMutation } from '@/hooks/useActionMutation'
import { queryKeys } from '@/lib/query-keys'
import { stringArrayOr, stringOr } from '@/lib/narrow'

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
 * Normalise a git pull response to an ActionResult.
 *
 * The backend is being migrated to the Action Truth Contract (B4). During the
 * migration window callers may receive either:
 *   - Legacy: { success: boolean, previousCommit, currentCommit, ... }
 *   - New:    { outcome, reason, details: { previousCommit, currentCommit, failedRedeploys } }
 *
 * Rules:
 *   - success==true AND previousCommit==currentCommit → no_change
 *   - success==true AND commits differ              → success
 *   - success==false                                → failed
 *   - ActionResult (new backend) → pass through
 *
 * Detail fields use `previousCommit`/`currentCommit` (matching both legacy wire
 * names and the new backend ActionResult details shape).
 */
export function normalisePullResult(raw: GitPullResult): ActionResult<{
  previousCommit?: string
  currentCommit?: string
  failedRedeploys?: Array<{ stack: string; reason: string }>
  changedFiles?: string[]
  redeployedStacks?: string[]
}> {
  if (isActionResult(raw)) {
    return raw as ActionResult<{
      previousCommit?: string
      currentCommit?: string
      failedRedeploys?: Array<{ stack: string; reason: string }>
      changedFiles?: string[]
      redeployedStacks?: string[]
    }>
  }

  // Legacy shape
  // agent-os-06c1: every field but `success` is `unknown`. The old shape
  // declared four REQUIRED strings/arrays on a body nothing validated, and
  // two of them were then handed to .slice(0, 7).
  const legacy = raw as {
    success: boolean
    previousCommit?: unknown
    currentCommit?: unknown
    changedFiles?: unknown
    redeployedStacks?: unknown
  }

  if (!legacy.success) {
    return { outcome: 'failed', reason: 'Git pull failed', details: {} }
  }

  // agent-os-06c1: narrowed, not asserted. The cast above claims four
  // REQUIRED fields on a body that reached us unvalidated, and unlike every
  // other site in that class this one calls .slice(0, 7) on two of them — so
  // a non-string here throws a TypeError rather than rendering oddly.
  const previousCommit = stringOr(legacy.previousCommit, '')
  const currentCommit = stringOr(legacy.currentCommit, '')
  const details = {
    previousCommit,
    currentCommit,
    changedFiles: stringArrayOr(legacy.changedFiles, []),
    redeployedStacks: stringArrayOr(legacy.redeployedStacks, []),
  }

  if (previousCommit === currentCommit) {
    return {
      outcome: 'no_change',
      reason: 'Already up to date',
      details,
    }
  }

  return {
    outcome: 'success',
    reason: `Pulled ${previousCommit.slice(0, 7)} → ${currentCommit.slice(0, 7)}`,
    details,
  }
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
      const raw = await gitApi.pull(stackId, redeploy)
      const normalised = normalisePullResult(raw)
      // Attach stackId so onResult can invalidate the per-stack git query
      return { ...normalised, _stackId: stackId }
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
