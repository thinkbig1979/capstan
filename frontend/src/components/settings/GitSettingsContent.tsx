import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useGitSettings, useUpdateGitSettings } from '@/hooks/useResources'
import { Eye, EyeOff } from 'lucide-react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'

export function GitSettingsContent() {
  const { data: gitSettings, isLoading } = useGitSettings()
  const updateGitSettings = useUpdateGitSettings()

  const [sshKey, setSshKey] = useState<string | undefined>(undefined)
  const [httpsUser, setHttpsUser] = useState<string | undefined>(undefined)
  const [httpsToken, setHttpsToken] = useState('')
  const [showToken, setShowToken] = useState(false)

  const effectiveSshKey = sshKey !== undefined ? sshKey : (gitSettings?.sshKey || '')
  const effectiveHttpsUser = httpsUser !== undefined ? httpsUser : (gitSettings?.httpsUser || '')

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
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
      // Branch rather than a conditional second argument: see the WHY at
      // UpdateScheduleContent.tsx's onError. The no-cause path stays a
      // single-argument call that a pre-existing test still pins.
      onError: (error) => {
        const cause = settingsSaveFault(error)
        if (cause) toast.error('Failed to save git settings', { description: cause })
        else toast.error('Failed to save git settings')
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
            {gitSettings?.hasHttpsToken && (
              <span className="ml-2 text-xs text-muted-foreground font-normal">(currently set)</span>
            )}
          </Label>
          <div className="flex items-center gap-2 max-w-md">
            <Input
              id="git-https-token"
              type={showToken ? 'text' : 'password'}
              placeholder={gitSettings?.hasHttpsToken ? 'Leave blank to keep current token' : 'ghp_xxxx or glpat-xxxx'}
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
          <p className="text-xs text-muted-foreground">
            Used as the default token for HTTPS git remotes. Individual stack credentials override these.
          </p>
        </div>
      </div>

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
