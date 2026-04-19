import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Download, GitBranch, ArrowUp, ArrowDown, FileWarning } from 'lucide-react'
import { useGitStatus, useGitPull } from '@/hooks/useGit'
import { toast } from 'sonner'
import { useState } from 'react'
import { ConfirmDialog } from '@/components/ConfirmDialog'

interface GitStatusProps {
  stackId: string
}

export function GitStatus({ stackId }: GitStatusProps) {
  const { data: gitStatus, isLoading, error } = useGitStatus(stackId)
  const pullMutation = useGitPull()
  const [showConfirmDialog, setShowConfirmDialog] = useState(false)
  const [confirmDialogProps, setConfirmDialogProps] = useState<{
    title: string
    description: string
    onConfirm: () => void
  } | null>(null)

  if (isLoading) {
    return (
      <div className="rounded-lg border bg-card p-4 space-y-3">
        <div className="flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-medium">Git Status</h3>
        </div>
        <div className="text-sm text-muted-foreground">Loading git status...</div>
      </div>
    )
  }

  if (error || !gitStatus) {
    return (
      <div className="rounded-lg border bg-card p-4 space-y-3">
        <div className="flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-medium">Git Status</h3>
        </div>
        <p className="text-sm text-muted-foreground">
          This directory is not a git repository.
        </p>
      </div>
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
    pullMutation.mutate(
      { stackId, redeploy },
      {
        onSuccess: (data) => {
          if (redeploy && data.redeployedStacks) {
            toast.success(
              `Pulled and redeployed ${data.redeployedStacks.length} stack(s): ${data.redeployedStacks.join(', ')}`,
            )
          } else {
            toast.success('Git pull completed successfully')
          }
        },
        onError: (error) => {
          const errorData = (error as any).response?.data
          if (errorData?.dirty) {
            toast.error('Cannot pull: working directory has uncommitted changes')
          } else if (errorData?.conflict) {
            toast.error('Pull failed: merge conflict detected')
          } else {
            toast.error('Failed to pull from remote')
          }
        },
      },
    )
  }

  return (
    <>
      <div className="rounded-lg border bg-card p-4 space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <GitBranch className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Git Status</h3>
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
              Git Pull & Redeploy
            </Button>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline" className="font-mono text-xs gap-1.5">
            <GitBranch className="h-3 w-3" />
            {gitStatus.branch}
          </Badge>

          {gitStatus.ahead > 0 && (
            <Badge variant="secondary" className="text-xs gap-1 text-green-600">
              <ArrowUp className="h-3 w-3" />
              {gitStatus.ahead} ahead
            </Badge>
          )}
          {gitStatus.behind > 0 && (
            <Badge variant="secondary" className="text-xs gap-1 text-yellow-600">
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
      </div>

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
