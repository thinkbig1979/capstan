import { useState, useEffect } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { KeyRound, ChevronDown, ChevronRight } from 'lucide-react'
import { directoriesApi } from '@/lib/api'
import { toast } from 'sonner'
import { queryKeys } from '@/lib/query-keys'

interface GitSettingsSectionProps {
  directoryPath: string
  remoteURL?: string
  open: boolean
  onToggle: () => void
}

export function GitSettingsSection({
  directoryPath,
  remoteURL,
  open,
  onToggle,
}: GitSettingsSectionProps) {
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
      const dir = dirs.find(d => d.path === directoryPath)
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
      queryClient.invalidateQueries({ queryKey: queryKeys.directories() })
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
        <KeyRound className={`h-3.5 w-3.5 ${hasCustomCreds ? 'text-success' : ''}`} />
        <span>Git Credentials</span>
        {hasCustomCreds && (
          <Badge variant="secondary" className="text-xs ml-1">{authType}</Badge>
        )}
      </button>

      {open && (
        <form onSubmit={(e) => { e.preventDefault(); saveMutation.mutate() }} className="mt-3 space-y-3 pl-5">
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
              type="submit"
              size="sm"
              className="h-7 text-xs"
              disabled={saveMutation.isPending}
            >
              {saveMutation.isPending ? 'Saving...' : 'Save Credentials'}
            </Button>
          </div>
        </form>
      )}
    </div>
  )
}
