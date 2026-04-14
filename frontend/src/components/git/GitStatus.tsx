import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Download } from 'lucide-react'
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
    return <div className="text-sm text-muted-foreground">Loading git status...</div>
  }

  if (error || !gitStatus) {
    return (
      <div className="text-sm text-muted-foreground">
        {error ? 'Failed to load git status' : 'Not a git repository'}
      </div>
    )
  }

  const handlePull = (redeploy = false) => {
    const isDirty = gitStatus.dirty
    const dirtyWarning = isDirty
      ? '\n\n⚠️ Warning: Your working directory has uncommitted changes. Pulling may cause conflicts or overwrite your changes.'
      : ''
    
    if (redeploy) {
      setConfirmDialogProps({
        title: 'Confirm Pull & Redeploy',
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
            toast.error('Cannot pull: working directory is dirty')
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
      <div className="flex items-center justify-between rounded-lg border bg-card p-4">
        <div className="flex items-center gap-3">
          <Badge variant="outline" className="font-mono text-sm">
            {gitStatus.branch}
          </Badge>

          <div className="flex items-center gap-2">
            {gitStatus.ahead > 0 && (
              <Badge variant="secondary" className="text-green-600">
                ↑ {gitStatus.ahead}
              </Badge>
            )}
            {gitStatus.behind > 0 && (
              <Badge variant="secondary" className="text-yellow-600">
                ↓ {gitStatus.behind}
              </Badge>
            )}
            {gitStatus.dirty && (
              <Badge variant="destructive" className="text-xs">
                dirty
              </Badge>
            )}
          </div>
        </div>

        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => handlePull(false)}
            disabled={pullMutation.isPending}
          >
            <Download className="mr-2 h-4 w-4" />
            Pull
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
