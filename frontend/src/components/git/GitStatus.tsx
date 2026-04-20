import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Download, GitBranch, ArrowUp, ArrowDown, FileWarning, KeyRound, ChevronDown, ChevronRight } from 'lucide-react'
import { useGitStatus, useGitPull } from '@/hooks/useGit'
import { toast } from 'sonner'
import { useState, useEffect } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { directoriesApi } from '@/lib/api'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { Stack, Directory } from '@/types'

interface GitStatusProps {
  stack: Stack
}

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
      { stackId: stack.id, redeploy },
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
              Pull & Redeploy
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

        <GitSettingsSection
          directoryPath={stack.directory}
          remoteURL={gitStatus.remote}
          open={settingsOpen}
          onToggle={() => setSettingsOpen(!settingsOpen)}
        />
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

function GitSettingsSection({
  directoryPath,
  remoteURL,
  open,
  onToggle,
}: {
  directoryPath: string
  remoteURL?: string
  open: boolean
  onToggle: () => void
}) {
  const queryClient = useQueryClient()
  const [authType, setAuthType] = useState('inherit')
  const [sshKeyPath, setSshKeyPath] = useState('')
  const [httpsUser, setHttpsUser] = useState('')
  const [httpsToken, setHttpsToken] = useState('')
  const [hasToken, setHasToken] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const isSSH = remoteURL?.startsWith('git@') || remoteURL?.startsWith('ssh://')
  const isHTTPS = remoteURL?.startsWith('https://') || remoteURL?.startsWith('http://')
  useEffect(() => {
    if (!open || loaded) return
    directoriesApi.list().then((dirs) => {
      const dir = (dirs as (Directory & { stackCount?: number })[]).find(d => d.path === directoryPath)
      if (dir) {
        setAuthType(dir.gitAuthType || 'inherit')
        setSshKeyPath(dir.gitSshKeyPath || '')
        setHttpsUser(dir.gitHttpsUser || '')
        setHasToken(dir.hasHttpsToken || false)
      }
      setLoaded(true)
    })
  }, [open, loaded, directoryPath])

  const saveMutation = useMutation({
    mutationFn: () => directoriesApi.updateCredentials(directoryPath, {
      authType,
      sshKeyPath: authType === 'ssh' ? sshKeyPath : undefined,
      httpsUser: authType === 'https' ? httpsUser : undefined,
      httpsToken: authType === 'https' ? httpsToken : undefined,
    }),
    onSuccess: () => {
      toast.success('Git credentials saved')
      queryClient.invalidateQueries({ queryKey: ['directories'] })
      setLoaded(false)
    },
    onError: () => toast.error('Failed to save credentials'),
  })

  const hasCustomCreds = authType !== 'inherit' && authType !== ''

  return (
    <div className="border-t pt-3 mt-1">
      <button
        type="button"
        className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground w-full"
        onClick={onToggle}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        <KeyRound className={`h-3.5 w-3.5 ${hasCustomCreds ? 'text-green-500' : ''}`} />
        <span>Git Credentials</span>
        {hasCustomCreds && (
          <Badge variant="secondary" className="text-xs ml-1">{authType}</Badge>
        )}
      </button>

      {open && (
        <div className="mt-3 space-y-3 pl-5">
          <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-3 items-center max-w-md">
            <Label className="text-sm text-right">Method</Label>
            <div>
              <Select value={authType} onValueChange={setAuthType}>
                <SelectTrigger className="h-8 text-sm">
                  <SelectValue placeholder="Select method" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="inherit">Use global settings</SelectItem>
                  <SelectItem value="ssh">SSH key</SelectItem>
                  <SelectItem value="https">HTTPS token</SelectItem>
                </SelectContent>
              </Select>
              {remoteURL && (
                <p className="text-xs text-muted-foreground mt-1">
                  Remote: {isSSH ? 'SSH' : isHTTPS ? 'HTTPS' : 'unknown'} ({remoteURL})
                </p>
              )}
            </div>

            {authType === 'ssh' && (
              <>
                <Label className="text-sm text-right">SSH Key</Label>
                <Input
                  type="text"
                  placeholder="/path/to/id_rsa (inside container)"
                  value={sshKeyPath}
                  onChange={(e) => setSshKeyPath(e.target.value)}
                  className="h-8 text-sm"
                />
              </>
            )}

            {authType === 'https' && (
              <>
                <Label className="text-sm text-right">Username</Label>
                <Input
                  type="text"
                  placeholder="git"
                  value={httpsUser}
                  onChange={(e) => setHttpsUser(e.target.value)}
                  className="h-8 text-sm"
                />
                <Label className="text-sm text-right">
                  Token
                  {hasToken && <span className="ml-1 text-xs text-muted-foreground font-normal">(set)</span>}
                </Label>
                <Input
                  type="password"
                  placeholder={hasToken ? 'Leave blank to keep current' : 'ghp_xxxx'}
                  value={httpsToken}
                  onChange={(e) => setHttpsToken(e.target.value)}
                  className="h-8 text-sm"
                />
              </>
            )}
          </div>

          <div className="flex justify-end">
            <Button
              size="sm"
              className="h-7 text-xs"
              disabled={saveMutation.isPending}
              onClick={() => saveMutation.mutate()}
            >
              {saveMutation.isPending ? 'Saving...' : 'Save Credentials'}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
