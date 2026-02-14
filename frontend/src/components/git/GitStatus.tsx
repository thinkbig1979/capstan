import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Download } from 'lucide-react'
import { useGitStatus, useGitPull } from '@/hooks/useGit'
import { toast } from 'sonner'

interface GitStatusProps {
  directoryPath: string
}

export function GitStatus({ directoryPath }: GitStatusProps) {
  const { data: gitStatus, isLoading, error } = useGitStatus(directoryPath)
  const pullMutation = useGitPull()

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
    pullMutation.mutate(
      { path: directoryPath, redeploy },
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
        onError: (error: { response?: { data?: { dirty?: boolean; conflict?: boolean } } }) => {
          if (error.response?.data?.dirty) {
            toast.error('Cannot pull: working directory is dirty')
          } else if (error.response?.data?.conflict) {
            toast.error('Pull failed: merge conflict detected')
          } else {
            toast.error('Failed to pull from remote')
          }
        },
      },
    )
  }

  return (
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
  )
}
