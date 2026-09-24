import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Download, GitBranch, ArrowUp, ArrowDown, FileWarning, AlertTriangle } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { useGitStatus, useGitPull } from '@/hooks/useGit'
import { useState } from 'react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { directoriesApi } from '@/lib/api'
import { causeOf, presentError } from '@/lib/error-handler'
import { queryKeys } from '@/lib/query-keys'
import type { Stack } from '@/types'
import { GitSettingsSection } from '@/components/git/GitSettingsSection'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'

// Why Pull is off whenever the status could not be read (agent-os-528x). A pull
// on a dirty tree is the dangerous operation, and a failed probe cannot say the
// tree is clean.
const PULL_BLOCKED_REASON =
  "Pull is disabled: the working tree's state could not be read, so uncommitted changes can't be ruled out."

interface GitStatusProps {
  stack: Stack
}

interface GitPullActionsProps {
  canPull: boolean
  onPull: (redeploy: boolean) => void
}

/** The two pull buttons, disabled with a stated reason whenever `canPull` is false. */
function GitPullActions({ canPull, onPull }: GitPullActionsProps) {
  return (
    <div className="flex gap-2">
      <Button variant="outline" size="sm" onClick={() => onPull(false)} disabled={!canPull}>
        <Download className="mr-2 h-4 w-4" />
        Git Pull
      </Button>
      <Button variant="outline" size="sm" onClick={() => onPull(true)} disabled={!canPull}>
        <Download className="mr-2 h-4 w-4" />
        Pull & Redeploy
      </Button>
    </div>
  )
}

/**
 * Compact git chip for the stack header. Renders nothing while the status is
 * loading and when no data arrived. A failed request renders an explicit
 * "status unknown" chip with Pull disabled (agent-os-528x). A directory that
 * is not a repository gets a quiet chip that says so and offers Rescan
 * (agent-os-omvy); a repository with no commits yet, and a bare repository,
 * each get an inert chip that says so (agent-os-m2g8). Details and pull actions live in a popover behind the full chip.
 */
export function GitStatus({ stack }: GitStatusProps) {
  const { data: gitStatus, error, refetch } = useGitStatus(stack.id)
  const pullMutation = useGitPull()
  const [showConfirmDialog, setShowConfirmDialog] = useState(false)
  const [confirmDialogProps, setConfirmDialogProps] = useState<{
    title: string
    description: string
    onConfirm: () => void
  } | null>(null)

  const [settingsOpen, setSettingsOpen] = useState(false)
  const [isRescanning, setIsRescanning] = useState(false)
  const queryClient = useQueryClient()

  // Backs the Rescan offered on the non-repo chip below. Directory scanning has
  // no per-stack form: `directoriesApi.scan` takes no arguments and rescans
  // every monitored directory. DashboardPage's Refresh posts to the same
  // endpoint, but do not read the two as equivalent: it refetches its own two
  // queries directly and then invalidates the stats counter, while this drops
  // four cache keys including the stack's git probe, which the dashboard has no
  // reason to touch and this cannot do without.
  //
  // The scan is the only thing that rewrites the cached `Stack.isGitRepo` the
  // badges elsewhere read: `command grep -rn "IsGitRepo" backend
  // --include=*.go` shows the field constructed only in services/scanner.go
  // (:1170, :1328), every other hit reading or persisting it, and UpsertStack
  // in database/stacks.go is an `INSERT OR REPLACE`, so a scan overwrites the
  // stored value outright rather than leaving a stale one behind.
  //
  // Hence four keys rather than one. That field rides on the directory rows as
  // well as the stack rows, and the dashboard's git badge reads the directories
  // query (DashboardPage.tsx -> DirectoriesTab.tsx), which is the surface the
  // button's own tooltip sends the user to look at. The same pairing is the
  // convention here: hooks/useCreateStack.ts and GitSettingsSection invalidate
  // directories after a directory-affecting mutation, and a full rescan is the
  // most directory-affecting mutation there is.
  //
  // One honest limit, considered rather than missed: for the majority of stacks
  // that simply have no git in them, this is a no-op nothing on screen can
  // show, and only a failure toasts. A successful no-op and a swallowed error
  // therefore look alike. The alternative is a spinner and a success toast on a
  // chip whose whole point is to stay quiet.
  const handleRescan = async () => {
    setIsRescanning(true)
    try {
      await directoriesApi.scan()
      // Every cache the scan can have changed: this stack's git probe, and the
      // stack and directory rows carrying `isGitRepo` for the badges on the
      // other pages.
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.git.all(stack.id) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.stacks() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.stack.all() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.directories() }),
      ])
    } catch (err) {
      presentError(err, { fallback: 'Rescan failed' })
    } finally {
      setIsRescanning(false)
    }
  }

  // THE PULL GATE (agent-os-528x), and the only place Pull is enabled. Every
  // term is required. `!error` matters even with data in hand: react-query
  // keeps the last good payload when a refetch fails, so a stale "clean" would
  // otherwise enable a pull on a tree nobody can currently read.
  const canPull =
    !error &&
    !!gitStatus &&
    gitStatus.isRepo &&
    gitStatus.hasCommits &&
    !gitStatus.isBare &&
    !pullMutation.isPending

  // A failed request with nothing we can still show (agent-os-528x). It used to
  // render nothing, folded together with "still loading", so a probe fault took
  // the branch, the commit and the Pull button away with no reason given. The
  // fields a stale non-repo or no-commits payload carries are not status, so
  // they fall here too rather than being shown as if current.
  //
  // No field is borrowed from the cached `stack.gitBranch` / `stack.gitDirty`.
  // Those come from the scanner, a different predicate from this live probe
  // (agent-os-a786), and a cached `gitDirty: false` is exactly the fabricated
  // "clean" this state exists to refuse.
  if (error && !(gitStatus?.isRepo && gitStatus.hasCommits)) {
    return <GitStatusUnknown error={error} canPull={canPull} onRetry={() => void refetch()} />
  }

  // Still loading, or no data. Blank on purpose: a loading chip would flash on
  // every stack header. `!gitStatus.isRepo` used to be an arm here and is now
  // its own branch below — folding it in rendered nothing for a non-git
  // directory (agent-os-omvy).
  if (!gitStatus) {
    return null
  }

  // A directory with no repository in it. The endpoint answers this with 200
  // `{isRepo: false}` rather than a 404 (agent-os-x40a), so it arrives as DATA
  // and never as `error` — which is the point: a 404 put a red failed request
  // in the console of every non-git stack for something nobody did wrong.
  //
  // Rendering nothing for it was a dead end (agent-os-omvy): the stack header
  // went blank with no statement of the fact. Quiet, though. For most stacks
  // this is an ordinary state and not a fault, so it gets the muted chip rather
  // than a warning.
  //
  // The Rescan on the chip clears a stale TRUE, and it is worth being exact
  // about the direction, because the opposite reading is intuitive and wrong.
  // This endpoint is a LIVE probe: handlers/git.go never consults the cached
  // `Stack.isGitRepo` (`command grep -n "IsGitRepo" backend/internal/handlers/
  // git.go` returns nothing) and derives `isRepo` from the real path at request
  // time. So a directory that BECOMES a repository answers `isRepo: true` on
  // the very next fetch, with no rescan, and this chip is never on screen for
  // it. The reverse persists: a directory whose `.git` has gone since the last
  // scan answers `isRepo: false` here while the dashboard and sidebar badges
  // still read the cached field and show a branch that no longer exists.
  // Rescan reconciles those two, which is why it invalidates the stack keys.
  //
  // Narrowing here also gives the ~130 lines below `GitRepoStatus` for free, so
  // a future field read on the non-repo branch is a compile error rather than
  // an `undefined` rendered into the chip.
  if (!gitStatus.isRepo) {
    return (
      <span
        className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-0.5 text-xs text-muted-foreground"
        aria-label="Git status: not a git repository"
      >
        <GitBranch className="h-3 w-3" aria-hidden="true" />
        not a git repository
        <button
          type="button"
          onClick={handleRescan}
          disabled={isRescanning}
          className="underline underline-offset-2 hover:text-foreground disabled:opacity-60"
          title="Rescans the monitored directories. Use it if this stack still shows a git branch elsewhere in the UI."
        >
          {isRescanning ? 'Rescanning…' : 'Rescan'}
        </button>
      </span>
    )
  }

  // A `git init`'d directory with no commits yet (agent-os-4a4a). It used to
  // arrive as an ERROR — a 404 that put a red entry in the console — and this
  // component rendered nothing for it. Rendering nothing is still the wrong
  // answer now that it arrives as data: an empty repository IS a repository,
  // and an operator who has just run `git init` would otherwise see the same
  // blank header as a stack with no git at all, with no way to tell which.
  //
  // Inert on purpose. There is no branch to name, no commit to show, no remote
  // to configure and nothing to pull, so the popover behind the chip below
  // would open on three empty rows.
  if (!gitStatus.hasCommits) {
    return (
      <span
        className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-0.5 text-xs font-mono text-muted-foreground"
        aria-label="Git status: repository initialised, no commits yet"
      >
        <GitBranch className="h-3 w-3" aria-hidden="true" />
        no commits
      </span>
    )
  }

  // A bare repository (agent-os-m2g8): a branch and a commit, and no work tree,
  // so there is nothing to call clean or dirty and nothing to pull into. The
  // server leaves those fields out rather than sending a zero, and this chip
  // is inert for the same reason the no-commits one is: the popover below
  // exists for Pull and its credentials. A failed refetch lands here too and
  // keeps the last branch and commit, as the repository chip does.
  if (gitStatus.isBare) {
    return (
      <span
        className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-0.5 text-xs font-mono text-muted-foreground"
        aria-label={`Git status: ${gitStatus.branch}, bare repository`}
        title={gitStatus.commitMessage}
      >
        <GitBranch className="h-3 w-3" aria-hidden="true" />
        {gitStatus.branch}
        {gitStatus.commitShort && <span>{gitStatus.commitShort}</span>}
        <span>· bare repo</span>
      </span>
    )
  }

  // From here on `gitStatus` is a repository with commits. `error` can still be
  // set: that is a refetch that failed after an earlier success, and the chip
  // keeps the branch and commit the server last sent but no longer claims
  // clean or dirty, ahead or behind (agent-os-528x).
  const statusKnown = !error

  const handlePull = (redeploy = false) => {
    if (!canPull) return
    const dirtyWarning = gitStatus.dirty
      ? `\n\nWarning: Your working directory has ${gitStatus.dirtyCount} uncommitted change${gitStatus.dirtyCount !== 1 ? 's' : ''}. Pulling may cause conflicts or overwrite your changes.`
      : ''

    if (redeploy) {
      setConfirmDialogProps({
        title: 'Confirm Git Pull & Redeploy',
        description: `This will pull the latest changes from the remote repository and redeploy all affected stacks.${dirtyWarning}\n\nThis operation will restart containers, which may cause brief downtime.`,
        onConfirm: () => executePull(true),
      })
    } else {
      setConfirmDialogProps({
        title: 'Confirm Pull',
        description: `This will pull the latest changes from the remote repository.${dirtyWarning}`,
        onConfirm: () => executePull(false),
      })
    }
    setShowConfirmDialog(true)
  }

  const executePull = (redeploy: boolean) => {
    // toastForResult is called by useActionMutation — no inline onSuccess toast needed.
    // The mutation's onResult handler in useGitPull handles partial/failed redeploy details.
    pullMutation.mutate({ stackId: stack.id, redeploy })
  }

  return (
    <>
      <Popover>
        <PopoverTrigger asChild>
          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-0.5 text-xs font-mono text-info hover:bg-accent transition-colors"
            aria-label={`Git status: ${gitStatus.branch}, ${!statusKnown ? 'status unknown' : gitStatus.dirty ? `${gitStatus.dirtyCount} uncommitted changes` : 'clean'}`}
          >
            <GitBranch className="h-3 w-3" aria-hidden="true" />
            {gitStatus.branch}
            {!statusKnown ? (
              <span className="text-warning">· status unknown</span>
            ) : (
              <span className={gitStatus.dirty ? 'text-warning' : 'text-muted-foreground'}>
                · {gitStatus.dirty ? `${gitStatus.dirtyCount} dirty` : 'clean'}
              </span>
            )}
            {statusKnown && gitStatus.ahead > 0 && (
              <span className="inline-flex items-center text-success">
                <ArrowUp className="h-3 w-3" aria-hidden="true" />
                {gitStatus.ahead}
              </span>
            )}
            {statusKnown && gitStatus.behind > 0 && (
              <span className="inline-flex items-center text-warning">
                <ArrowDown className="h-3 w-3" aria-hidden="true" />
                {gitStatus.behind}
              </span>
            )}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-96 space-y-3">
          {!statusKnown && (
            <RefreshFailedNotice what="the git status" onRetry={() => void refetch()} />
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline" className="font-mono text-xs gap-1.5">
              <GitBranch className="h-3 w-3" />
              {gitStatus.branch}
            </Badge>

            {statusKnown && gitStatus.ahead > 0 && (
              <Badge variant="secondary" className="text-xs gap-1 text-success">
                <ArrowUp className="h-3 w-3" />
                {gitStatus.ahead} ahead
              </Badge>
            )}
            {statusKnown && gitStatus.behind > 0 && (
              <Badge variant="secondary" className="text-xs gap-1 text-warning">
                <ArrowDown className="h-3 w-3" />
                {gitStatus.behind} behind
              </Badge>
            )}

            {statusKnown && gitStatus.dirty && (
              <Badge variant="destructive" className="text-xs gap-1">
                <FileWarning className="h-3 w-3" />
                {gitStatus.dirtyCount} uncommitted change{gitStatus.dirtyCount !== 1 ? 's' : ''}
              </Badge>
            )}

            {gitStatus.commitShort && (
              <span className="text-xs text-muted-foreground font-mono" title={gitStatus.commitMessage}>
                {gitStatus.commitShort}
              </span>
            )}
          </div>

          <GitPullActions canPull={canPull} onPull={handlePull} />
          {!statusKnown && (
            <p className="text-xs text-muted-foreground">{PULL_BLOCKED_REASON}</p>
          )}

          <GitSettingsSection
            directoryPath={stack.directory}
            remoteURL={gitStatus.remote}
            open={settingsOpen}
            onToggle={() => setSettingsOpen(!settingsOpen)}
          />
        </PopoverContent>
      </Popover>

      {showConfirmDialog && confirmDialogProps && (
        <ConfirmDialog
          open={showConfirmDialog}
          onOpenChange={setShowConfirmDialog}
          title={confirmDialogProps.title}
          description={confirmDialogProps.description}
          confirmText="Confirm"
          onConfirm={confirmDialogProps.onConfirm}
          isDangerous={gitStatus.dirty}
        />
      )}
    </>
  )
}

interface GitStatusUnknownProps {
  error: unknown
  /** The component's one Pull gate, false in every state that reaches here. */
  canPull: boolean
  onRetry: () => void
}

/**
 * The git status could not be read and nothing from it can be shown
 * (agent-os-528x). Never "clean" and never nothing: the chip says the status
 * is unknown, the popover says why, and Pull is present but disabled with the
 * reason, so the operator can see the action exists and why it is off.
 */
function GitStatusUnknown({ error, canPull, onRetry }: GitStatusUnknownProps) {
  const cause = causeOf(error)
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-0.5 text-xs text-warning hover:bg-accent transition-colors"
          aria-label="Git status: unknown"
        >
          <AlertTriangle className="h-3 w-3" aria-hidden="true" />
          git status unknown
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-96 space-y-3">
        <div className="space-y-1 text-sm">
          <p>Could not read the git status.</p>
          {cause && <p className="text-muted-foreground">{cause}</p>}
        </div>
        <GitPullActions canPull={canPull} onPull={() => {}} />
        <p className="text-xs text-muted-foreground">{PULL_BLOCKED_REASON}</p>
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>
          Retry
        </Button>
      </PopoverContent>
    </Popover>
  )
}
