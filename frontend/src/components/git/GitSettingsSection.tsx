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
import { KeyRound, ChevronDown, ChevronRight, AlertTriangle } from 'lucide-react'
import { directoriesApi } from '@/lib/api'
import { toast } from 'sonner'
import { queryKeys } from '@/lib/query-keys'
import type { DirectoryCredentialStatusValue } from '@/types'

/**
 * The credentials save's two ACTIONABLE wire codes. UpdateCredentials MINTS four
 * — VALIDATION_ERROR and INTERNAL_ERROR, which say nothing an operator can act
 * on and keep the generic sentence, plus these two, which do:
 *
 *   NOT_FOUND               404, "Directory not found" — the directory this form
 *                           is editing is gone from the DB, so no amount of
 *                           retyping the token will help (directories.go:169,
 *                           inside UpdateCredentials).
 *   ENCRYPTION_KEY_MISSING  422, EncryptionUnavailableMessage (respond.go:222),
 *                           minted by respondIfEncryptionUnavailable and reached
 *                           from this endpoint's own write at directories.go:185.
 *                           It carries a cause AND a recovery ("Set STORAGE_KEY
 *                           (or JWT_SECRET) ... and restart Capstan"), and
 *                           respond.go names git_https_token — this form's own
 *                           payload — as a trigger.
 *
 * KEY ON THE WIRE VALUE, not on the Go identifier: the constant is
 * models.ErrEncryptionUnavailable and its VALUE is "ENCRYPTION_KEY_MISSING"
 * (the same trap as models.ErrValidation = "VALIDATION_ERROR").
 *
 * WHAT THE HANDLER MINTS IS NOT WHAT THE ROUTE ANSWERS, and the difference is
 * the reason this reads as an ALLOW-LIST rather than as a list of the codes to
 * skip. PUT /directories/credentials is registered on the protected group
 * (main.go:523 -> directories.go:32), which sits behind three rejecting
 * middlewares (main.go:511-513) plus the engine-level stack at main.go:455-463,
 * and those write their own JSON bodies as bare gin.H — invisible to any sweep
 * keyed on models.NewAppError. MEASURED at this commit, the route can answer
 * with ELEVEN distinct codes: the four above, plus SESSION_EXPIRED
 * (auth.go:202,215,217,232,248,270,282) and FORBIDDEN (auth.go:181), INVALID_KEY
 * (ratelimit.go:298) and RATE_LIMITED (ratelimit.go:309), and CSRF_COOKIE_MISSING
 * / CSRF_TOKEN_MISSING / CSRF_TOKEN_INVALID (csrf.go:60,71,96). Two further
 * middleware sites add EMITTERS rather than codes and so do not raise the count:
 * validation.go:126 mints VALIDATION_ERROR and recovery.go:54 and auth.go:257
 * mint INTERNAL_ERROR, both already in the handler's four. All seven of the
 * middleware-only codes fall through this function's `null` to the bare generic
 * sentence, which is correct — none is special-cased and none needs to be.
 *
 * Keys on the CODE, never on "did something carry a message", and renders
 * exactly ONE field, `message` — not `details`, not the whole body. This is the
 * git CREDENTIALS endpoint, so the allow-list is the bound on what server text
 * can ever reach a toast from here. Stated accurately: there is NO leak being
 * repaired. Every message UpdateCredentials emits today is a fixed Go literal,
 * none interpolates req.Path/HTTPSUser/HTTPSToken or an err, AppError.Cause is
 * `json:"-"`, and directories.go:211 blanks GitHTTPSToken before the 200. The
 * allow-list is future-proofing against a message added to this endpoint later.
 *
 * BUT BE PRECISE ABOUT WHAT IS BOUNDED: the allow-list bounds the CODE, and
 * NOTHING HERE BOUNDS THE MESSAGE. If a NOT_FOUND or ENCRYPTION_KEY_MISSING
 * message ever becomes interpolated, this predicate renders it verbatim. That is
 * not hypothetical — the interpolating idiom is already in this codebase and a
 * contributor will copy it; stack_crud.go:125 builds an AppError message with
 * fmt.Sprintf carrying client-supplied text. The safety of this site rests on
 * the two allowed messages staying fixed Go literals, so anyone making one of
 * them dynamic has to revisit this function.
 *
 * Deliberately NOT routed through classifyError(), and the reason is SPECIFIC TO
 * THIS SITE's two statuses rather than the 5xx one this prohibition is usually
 * argued from — neither code here is a 5xx, so that argument would not apply.
 * MEASURED: classifyError's 404 branch (error-handler.ts:170) hardcodes "The
 * requested resource was not found" and discards `message`, which is exactly the
 * discard this function exists to stop — so routing NOT_FOUND through it would
 * defeat the fix. Its 422 branch (error-handler.ts:193-207) would NOT: with no
 * `details` on the body, fieldMessage falls back to `message` and the server's
 * recovery sentence would survive. The prohibition is therefore not uniform
 * across the two codes, and it is written out rather than asserted flatly so
 * that nobody later "simplifies" this into a classifyError call on the strength
 * of the 422 half. One code-keyed predicate covering both is the point.
 *
 * Module-private rather than an arm on lib/backup-repo-fault.ts's repoFaultFrom,
 * and that FOLLOWS the house reasoning rather than departing from it.
 * repoFaultFrom is scoped to the three backup repository codes and is read by
 * the stack Backups panel and both backup-settings toasts, so a NOT_FOUND arm
 * there would change those consumers' rendering as a side effect of fixing this
 * form. useBackupActions.ts reads VALIDATION_ERROR locally for exactly that
 * reason, and its module-private `validationMessage` — including the docblock
 * re-teaching this same wire-value trap — is the shape copied here. The
 * duplication that put repoFaultFrom in lib/ does not arise: one endpoint, one
 * consumer, no second copy to drift.
 */
function credentialSaveFault(error: unknown): string | null {
  if (!error || typeof error !== 'object') return null
  const body = error as { code?: string; message?: string }
  if (body.code !== 'NOT_FOUND' && body.code !== 'ENCRYPTION_KEY_MISSING') return null
  return body.message || null
}

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
  const [credentialStatus, setCredentialStatus] = useState<DirectoryCredentialStatusValue | null>(null)

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
    // A separate probe rather than a field on ConfiguredDir: it decrypts the
    // stored token to tell "unreadable" (rotated STORAGE_KEY) apart from
    // "none", which directoriesApi.list()/ConfiguredDir deliberately never
    // does. Kept inside this same open-gated effect so it fires once per
    // disclosure expansion, not on every page load or per stack. See
    // agent-os-8a5.
    directoriesApi.credentialStatus(directoryPath)
      .then((result) => setCredentialStatus(result.status))
      .catch(() => setCredentialStatus(null))
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
    // Takes the error. It used to take nothing, which made both codes above
    // unreadable in principle rather than merely unread: there was no unused
    // variable and no type error for anyone to notice (agent-os-82lk, the same
    // shape agent-os-nhiv and agent-os-3wyv repaired in backup settings). A real
    // production 404 sat behind this sentence for months (agent-os-p7r).
    //
    // The generic sentence stays as the TITLE and the server's own message
    // arrives as the description, so a code outside the allow-list renders
    // exactly what it rendered before.
    onError: (error) => {
      const cause = credentialSaveFault(error)
      toast.error('Failed to save credentials', cause ? { description: cause } : undefined)
    },
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
                <SelectTrigger className="h-8 text-sm" aria-label="Authentication method">
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

            {credentialStatus === 'unreadable' && (
              <div className="col-span-2 flex items-center gap-1.5 text-xs text-destructive">
                <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                Stored credential can&apos;t be decrypted (the encryption key may have changed). Re-enter the token below.
              </div>
            )}

            {credentialStatus === 'empty' && (
              <div className="col-span-2 flex items-center gap-1.5 text-xs text-warning">
                <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                HTTPS auth is selected but no token is saved. Git operations against this remote will fail until one is added.
              </div>
            )}

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
