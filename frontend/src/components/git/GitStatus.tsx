import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Download, GitBranch, ArrowUp, ArrowDown, FileWarning } from 'lucide-react'
import { useGitStatus, useGitPull } from '@/hooks/useGit'
import { useState } from 'react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { Stack } from '@/types'
import { GitSettingsSection } from '@/components/git/GitSettingsSection'

interface GitStatusProps {
  stack: Stack
}

/**
 * Compact git chip for the stack header. Renders nothing while loading and
 * nothing at all for non-git directories; a repository with no commits yet gets
 * an inert chip that says so. Details and pull actions live in a popover behind
 * the full chip.
 */
export function GitStatus({ stack }: GitStatusProps) {
  const { data: gitStatus, isLoading, error } = useGitStatus(stack.id)
  const pullMutation = useGitPull()
  const [showConfirmDialog, setShowConfirmDialog] = useState(false)
  const [confirmDialogProps, setConfirmDialogProps] = useState<{
    title: string
    description: string
    onConfirm: () => void
  } | null>(null)

  const [settingsOpen, setSettingsOpen] = useState(false)

  // `!gitStatus.isRepo` is the whole of the non-git handling. The endpoint now
  // answers a genuine non-repo with 200 `{isRepo: false}` rather than a 404
  // (agent-os-x40a), so that case arrives as DATA and never as `error` — which
  // is the point: a 404 put a red failed request in the console of every
  // non-git stack for something nobody did wrong.
  //
  // Narrowing on it also gives the ~130 lines below `GitRepoStatus` for free,
  // so a future field read on the non-repo branch is a compile error rather
  // than an `undefined` rendered into the chip.
  if (isLoading || error || !gitStatus || !gitStatus.isRepo) {
    return null
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

  const handlePull = (redeploy = false) => {
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
            aria-label={`Git status: ${gitStatus.branch}, ${gitStatus.dirty ? `${gitStatus.dirtyCount} uncommitted changes` : 'clean'}`}
          >
            <GitBranch className="h-3 w-3" aria-hidden="true" />
            {gitStatus.branch}
            <span className={gitStatus.dirty ? 'text-warning' : 'text-muted-foreground'}>
              · {gitStatus.dirty ? `${gitStatus.dirtyCount} dirty` : 'clean'}
            </span>
            {gitStatus.ahead > 0 && (
              <span className="inline-flex items-center text-success">
                <ArrowUp className="h-3 w-3" aria-hidden="true" />
                {gitStatus.ahead}
              </span>
            )}
            {gitStatus.behind > 0 && (
              <span className="inline-flex items-center text-warning">
                <ArrowDown className="h-3 w-3" aria-hidden="true" />
                {gitStatus.behind}
              </span>
            )}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-96 space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline" className="font-mono text-xs gap-1.5">
              <GitBranch className="h-3 w-3" />
              {gitStatus.branch}
            </Badge>

            {gitStatus.ahead > 0 && (
              <Badge variant="secondary" className="text-xs gap-1 text-success">
                <ArrowUp className="h-3 w-3" />
                {gitStatus.ahead} ahead
              </Badge>
            )}
            {gitStatus.behind > 0 && (
              <Badge variant="secondary" className="text-xs gap-1 text-warning">
                <ArrowDown className="h-3 w-3" />
                {gitStatus.behind} behind
              </Badge>
            )}

            {gitStatus.dirty && (
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

          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => handlePull(false)}
              disabled={pullMutation.isPending}
            >
              <Download className="mr-2 h-4 w-4" />
              Git Pull
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => handlePull(true)}
              disabled={pullMutation.isPending}
            >
              <Download className="mr-2 h-4 w-4" />
              Pull & Redeploy
            </Button>
          </div>

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
