import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { useGitSettings, useUpdateGitSettings } from '@/hooks/useResources'
import { Eye, EyeOff } from 'lucide-react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { presentFault } from '@/lib/error-handler'

export function GitSettingsContent() {
  const { data: gitSettings, isLoading, isError, refetch } = useGitSettings()
  const updateGitSettings = useUpdateGitSettings()

  const [sshKey, setSshKey] = useState<string | undefined>(undefined)
  const [httpsUser, setHttpsUser] = useState<string | undefined>(undefined)
  const [httpsToken, setHttpsToken] = useState('')
  const [showToken, setShowToken] = useState(false)

  const effectiveSshKey = sshKey !== undefined ? sshKey : (gitSettings?.sshKey || '')
  const effectiveHttpsUser = httpsUser !== undefined ? httpsUser : (gitSettings?.httpsUser || '')
  // hasHttpsToken is also true for a stored token the server could not read
  // (GetGitSettings), so "currently set" alone would claim a usable token
  // (agent-os-pjos).
  const tokenUnreadable = gitSettings?.httpsTokenUnreadable === true
  const tokenReadable = Boolean(gitSettings?.hasHttpsToken) && !tokenUnreadable

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  // agent-os-gs2y: no form without the stored settings. An empty form here
  // read as "nothing is configured" and its Save wrote over credentials the
  // operator never saw.
  if (!gitSettings && isError) {
    return (
      <LoadFailedNotice
        what="the git settings"
        consequence="Saving is disabled until they load."
        onRetry={() => void refetch()}
      />
    )
  }

  const handleSave = () => {
    const data: { sshKey?: string; httpsUser?: string; httpsToken?: string } = {}
    if (effectiveSshKey) data.sshKey = effectiveSshKey
    if (effectiveHttpsUser) data.httpsUser = effectiveHttpsUser
    if (httpsToken) data.httpsToken = httpsToken
    updateGitSettings.mutate(data, {
      onSuccess: () => {
        toast.success('Git settings saved')
        setHttpsToken('')
      },
      // Takes the error (agent-os-zlw0). UpdateGitSettings refuses pasted key
      // material with a 400 naming the mistake, and answers 422
      // ENCRYPTION_KEY_MISSING on the token write with the recovery in it — both
      // were discarded by the zero-arity callback.
      // presentFault keeps the no-cause path a SINGLE-argument call, which a
      // pre-existing test still pins: see the WHY at UpdateScheduleContent's
      // onError.
      onError: (error) => {
        // presentFault, NOT presentError (agent-os-5g8a): the cause is read by
        // settingsSaveFault, which is CODE-keyed and deliberately not
        // classifyError. The full argument, measured, is in presentFault's
        // docblock; the short form is that swapping the key is not this
        // change's decision to take.
        presentFault('Failed to save git settings', settingsSaveFault(error))
      },
    })
  }

  return (
    <form onSubmit={(e) => { e.preventDefault(); handleSave() }} className="space-y-6">
      <div className="space-y-4">
        <h3 className="text-lg font-medium">SSH</h3>
        <div className="space-y-2">
          <Label htmlFor="git-ssh-key">SSH Private Key Path</Label>
          <Input
            id="git-ssh-key"
            type="text"
            placeholder="/path/to/id_rsa (inside container)"
            value={effectiveSshKey}
            onChange={(e) => setSshKey(e.target.value)}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">
            Path to the default SSH private key used for git operations. Must be accessible inside the container.
          </p>
        </div>
      </div>

      <div className="space-y-4 pt-4 border-t">
        <h3 className="text-lg font-medium">HTTPS</h3>
        <div className="space-y-2">
          <Label htmlFor="git-https-user">Username</Label>
          <Input
            id="git-https-user"
            type="text"
            placeholder="git"
            value={effectiveHttpsUser}
            onChange={(e) => setHttpsUser(e.target.value)}
            className="max-w-md"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="git-https-token">
            Personal Access Token
            {tokenReadable && (
              <span className="ml-2 text-xs text-muted-foreground font-normal">(currently set)</span>
            )}
          </Label>
          <div className="flex items-center gap-2 max-w-md">
            <Input
              id="git-https-token"
              type={showToken ? 'text' : 'password'}
              placeholder={
                tokenUnreadable
                  ? 'Enter the token again to replace it'
                  : tokenReadable
                    ? 'Leave blank to keep current token'
                    : 'ghp_xxxx or glpat-xxxx'
              }
              value={httpsToken}
              onChange={(e) => setHttpsToken(e.target.value)}
              className="flex-1"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => setShowToken((v) => !v)}
              title={showToken ? 'Hide token' : 'Reveal token'}
              aria-label={showToken ? 'Hide access token' : 'Reveal access token'}
            >
              {showToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </Button>
          </div>
          {tokenUnreadable && (
            <p className="text-xs text-destructive">
              A token is stored but could not be read. Enter it again to replace it.
            </p>
          )}
          <p className="text-xs text-muted-foreground">
            Used as the default token for HTTPS git remotes. Individual stack credentials override these.
          </p>
        </div>
      </div>

      {/*
        * agent-os-vs6c. A failed REFETCH keeps the last gitSettings TanStack
        * holds, and the untouched fields above fall back to it, so Save writes
        * those values back. Kept (they are real values the server sent) but
        * disclosed beside the control that submits them. Gated on
        * `gitSettings` because the copy says "the last ones the server sent",
        * which is false when the first load failed and nothing was ever sent.
        */}
      {isError && gitSettings && (
        <RefreshFailedNotice
          what="the git settings"
          beforeSave
          onRetry={() => void refetch()}
        />
      )}
      <div className="flex justify-end">
        <Button type="submit" disabled={updateGitSettings.isPending}>
          {updateGitSettings.isPending ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Saving...
            </>
          ) : (
            'Save Git Settings'
          )}
        </Button>
      </div>
    </form>
  )
}
