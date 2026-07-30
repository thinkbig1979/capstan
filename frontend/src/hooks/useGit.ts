import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { gitApi, type GitPullResult } from '@/lib/api'
import { isActionResult, type ActionResult } from '@/lib/action-result'
import { useActionMutation } from '@/hooks/useActionMutation'
import { queryKeys } from '@/lib/query-keys'

export function useGitStatus(stackId: string) {
  return useQuery({
    queryKey: queryKeys.git.all(stackId),
    queryFn: () => gitApi.status(stackId),
    staleTime: 60000,
    // A non-git stack returns a definitive 404 — retrying just repeats the error
    // in the console/network tab. The panel hides on first failure either way.
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
  const legacy = raw as {
    success: boolean
    previousCommit: string
    currentCommit: string
    changedFiles: string[]
    redeployedStacks: string[]
  }

  if (!legacy.success) {
    return { outcome: 'failed', reason: 'Git pull failed', details: {} }
  }

  if (legacy.previousCommit === legacy.currentCommit) {
    return {
      outcome: 'no_change',
      reason: 'Already up to date',
      details: {
        previousCommit: legacy.previousCommit,
        currentCommit: legacy.currentCommit,
        changedFiles: legacy.changedFiles,
        redeployedStacks: legacy.redeployedStacks,
      },
    }
  }

  return {
    outcome: 'success',
    reason: `Pulled ${legacy.previousCommit.slice(0, 7)} → ${legacy.currentCommit.slice(0, 7)}`,
    details: {
      previousCommit: legacy.previousCommit,
      currentCommit: legacy.currentCommit,
      changedFiles: legacy.changedFiles,
      redeployedStacks: legacy.redeployedStacks,
    },
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
        const failedRedeploys = (result.details as {
          failedRedeploys?: Array<{ stack: string; reason: string }>
        } | undefined)?.failedRedeploys ?? []
        if (failedRedeploys.length > 0) {
          const names = failedRedeploys.map((f) => f.stack).join(', ')
          toast.warning(`Failed to redeploy: ${names}`)
        }
      }
    },
  })
}
