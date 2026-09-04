import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import { CheckCircle2, Eye, EyeOff, KeyRound, XCircle } from 'lucide-react'
import type { BackupSettings } from '@/types'
import { SourceBadge } from './SourceBadge'

interface RepositorySectionProps {
  settings: BackupSettings
  repository: string
  onRepositoryChange: (value: string) => void
  password: string
  onPasswordChange: (value: string) => void
  showPassword: boolean
  onTogglePasswordReveal: () => void
  onClearPassword: () => void
  isSaving: boolean
  onInitRepo: () => void
  isInitializing: boolean
}

export function RepositorySection({
  settings,
  repository,
  onRepositoryChange,
  password,
  onPasswordChange,
  showPassword,
  onTogglePasswordReveal,
  onClearPassword,
  isSaving,
  onInitRepo,
  isInitializing,
}: RepositorySectionProps) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">Repository</h3>
        <HelpHint
          label="restic repository"
          title="restic repository"
          side="right"
          href="https://github.com/thinkbig1979/capstan/blob/main/docs/how-to/configure-backups.md#configuration"
        >
          <p>
            Backups run through restic, which stores them deduplicated and encrypted in a
            repository.
          </p>
          <p>
            Give it a path inside the container and a password, then initialize it once before
            the first backup.
          </p>
        </HelpHint>
      </div>

      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor="backup-repository">Repository path</Label>
          <SourceBadge source={settings.repositorySource} />
        </div>
        <Input
          id="backup-repository"
          type="text"
          placeholder="/data/restic-repo (inside container)"
          value={repository}
          onChange={(e) => onRepositoryChange(e.target.value)}
          className="max-w-md"
          aria-describedby={
            settings.hasEmbeddedCredential
              ? 'backup-repository-credential-hint backup-repository-hint'
              : 'backup-repository-hint'
          }
        />
        {/*
          The field stays an ordinary editable input; this is the only thing that
          tells the operator a credential is hidden inside the value they are
          looking at. Gated on the flag, never always-on: a warning that shows on
          every repository is one operators learn to skip past, which costs more
          than it saves on the repositories that do carry a credential.
        */}
        {settings.hasEmbeddedCredential && (
          <p
            id="backup-repository-credential-hint"
            className="flex items-start gap-1.5 text-xs text-warning"
          >
            <KeyRound className="h-3.5 w-3.5 mt-0.5 shrink-0" aria-hidden="true" />
            <span>
              A credential is embedded in this value and is shown here as <code>***</code>. To
              change any part of it, re-enter the full URI including the credential. Saving while{' '}
              <code>***</code> is still in the field is rejected, so the stored credential stays
              intact.
            </span>
          </p>
        )}
        <p id="backup-repository-hint" className="text-xs text-muted-foreground">
          Local path for the restic repository. Must be accessible inside the container.
          Leave blank to revert to the <code>RESTIC_REPOSITORY</code> environment variable.
        </p>
      </div>

      {/* Password field — masked behind env-unlock */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor="backup-password">
            Repository password
            {settings.hasPassword && (
              <span className="ml-1 text-xs text-muted-foreground font-normal">
                (currently set)
              </span>
            )}
          </Label>
          <SourceBadge source={settings.passwordSource} />
        </div>
        <div className="flex items-center gap-2 max-w-md">
          <Input
            id="backup-password"
            type={showPassword ? 'text' : 'password'}
            placeholder={settings.hasPassword ? 'Leave blank to keep current password' : 'Enter restic password'}
            value={password}
            onChange={(e) => onPasswordChange(e.target.value)}
            className="flex-1"
            aria-describedby="backup-password-hint"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={onTogglePasswordReveal}
            title={showPassword ? 'Hide password' : 'Reveal password'}
            aria-label={showPassword ? 'Hide backup password' : 'Reveal backup password'}
          >
            {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </Button>
        </div>
        <p id="backup-password-hint" className="text-xs text-muted-foreground">
          Leave blank to keep the current password unchanged. Corresponds to{' '}
          <code>RESTIC_PASSWORD</code>.
        </p>
        {settings.hasPassword && settings.passwordSource === 'db' && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive px-0 h-auto"
            onClick={onClearPassword}
            disabled={isSaving}
          >
            Clear saved password
          </Button>
        )}
      </div>

      {/* Init repo + status */}
      <div className="flex items-center gap-3 flex-wrap">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onInitRepo}
          disabled={isInitializing || !settings.resticAvailable}
          title={!settings.resticAvailable ? 'restic not available' : undefined}
        >
          {isInitializing ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Initializing…
            </>
          ) : (
            'Initialize repository'
          )}
        </Button>
        {settings.repositoryInitialized ? (
          <span className="inline-flex items-center gap-1.5 text-sm text-success font-medium">
            <CheckCircle2 className="h-4 w-4" />
            Initialized
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
            <XCircle className="h-4 w-4" />
            Not initialized
          </span>
        )}
      </div>
    </div>
  )
}
