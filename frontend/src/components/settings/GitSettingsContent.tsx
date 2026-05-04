import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useGitSettings, useUpdateGitSettings } from '@/hooks/useResources'
import { toast } from 'sonner'

export function GitSettingsContent() {
  const { data: gitSettings, isLoading } = useGitSettings()
  const updateGitSettings = useUpdateGitSettings()

  const [sshKey, setSshKey] = useState<string | undefined>(undefined)
  const [httpsUser, setHttpsUser] = useState<string | undefined>(undefined)
  const [httpsToken, setHttpsToken] = useState('')

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
      onError: () => toast.error('Failed to save git settings'),
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
          <Input
            id="git-https-token"
            type="password"
            placeholder={gitSettings?.hasHttpsToken ? 'Leave blank to keep current token' : 'ghp_xxxx or glpat-xxxx'}
            value={httpsToken}
            onChange={(e) => setHttpsToken(e.target.value)}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">
            Used as the default token for HTTPS git remotes. Individual stack credentials override these.
          </p>
        </div>
      </div>

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
    </form>
  )
}
