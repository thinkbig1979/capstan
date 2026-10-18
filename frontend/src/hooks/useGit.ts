import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { gitApi, type GitCommitRequest } from '@/lib/api'
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
    // is gone) and STACK_NOT_FOUND (unknown stackId). Neither is a transient fault,
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

// The commit-and-push reads (agent-os-k81p.3). Keyed under queryKeys.git.all,
// so invalidating that prefix after a pull, commit or push refreshes them too.
const changesKey = (stackId: string) => [...queryKeys.git.all(stackId), 'changes'] as const
const changesDiffKey = (stackId: string, path: string) => [...changesKey(stackId), 'diff', path] as const

/**
 * GET /git/changes: the tracked files a commit from this stack's folder could
 * include, and what decides whether a commit or push can run. `enabled`
 * exists so a caller reads it only when a dialog or panel is actually open.
 */
export function useGitChanges(stackId: string, enabled = true) {
  return useQuery({
    queryKey: changesKey(stackId),
    queryFn: () => gitApi.changes(stackId),
    enabled,
    // A save changes this list without touching git, and the save must make no
    // git call, so the list is re-read whenever a panel or the dialog mounts
    // rather than served from a cache up to staleTime old (review R3 S3).
    refetchOnMount: 'always',
    // Every answer but a fault is a 4xx that repeats on retry.
    retry: false,
  })
}

/**
 * GET /git/changes/diff for one listed file. Never called for an env file:
 * the caller passes `null` for one, and the server hides it as well (D136).
 */
export function useGitChangesDiff(stackId: string, path: string | null) {
  return useQuery({
    queryKey: changesDiffKey(stackId, path ?? ''),
    queryFn: () => gitApi.changesDiff(stackId, path ?? ''),
    enabled: path !== null,
    // Same as useGitChanges: a save since the last read changes the diff.
    refetchOnMount: 'always',
    retry: false,
  })
}

/**
 * The git queries a commit or push can have changed. Run on every outcome: a
 * 207 partial DID commit, and a refusal can mean the change list we showed was
 * stale.
 */
function useInvalidateGit() {
  const queryClient = useQueryClient()
  return (stackId: string) => {
    queryClient.invalidateQueries({ queryKey: queryKeys.git.all(stackId) })
  }
}

/** POST /git/commit, optionally pushing. Toasts come from useActionMutation. */
export function useGitCommit() {
  const invalidate = useInvalidateGit()
  return useActionMutation({
    mutationFn: (req: GitCommitRequest) => gitApi.commit(req),
    errorTitle: 'Commit failed',
    onResult: (_r, vars) => invalidate(vars.stackId),
    onError: (_err, vars) => invalidate(vars.stackId),
  })
}

/** POST /git/push. */
export function useGitPush() {
  const invalidate = useInvalidateGit()
  return useActionMutation({
    mutationFn: ({ stackId }: { stackId: string }) => gitApi.push(stackId),
    errorTitle: 'Push failed',
    onResult: (_r, vars) => invalidate(vars.stackId),
    onError: (_err, vars) => invalidate(vars.stackId),
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
