'use client'
import { useState, useEffect } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { BackupStatusCard } from '@/components/dashboard/BackupStatusCard'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'
import { HelpHint } from '@/components/ui/help-hint'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuth } from '@/hooks/useAuth'
import {
  useBackupSettings,
  useUpdateBackupSettings,
  useInitRepo,
  useTestCloud,
} from '@/hooks/useBackup'
import { toast } from 'sonner'
import { AlertCircle, CheckCircle2, Eye, EyeOff, XCircle } from 'lucide-react'
import type { BackupSettings } from '@/types'

// ─── Source badge ─────────────────────────────────────────────────────────────

type Source = 'env' | 'db' | 'default'

function SourceBadge({ source }: { source: Source }) {
  const labels: Record<Source, string> = {
    env: 'from environment',
    db: 'saved',
    default: 'default',
  }
  const variants: Record<Source, 'default' | 'secondary' | 'outline'> = {
    env: 'default',
    db: 'secondary',
    default: 'outline',
  }
  return (
    <Badge variant={variants[source]} className="text-xs font-normal">
      {labels[source]}
    </Badge>
  )
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/** Build update payload — only include fields that differ from remote settings. */
function buildPayload(
  remote: BackupSettings,
  draft: Draft,
  password: string,
): Parameters<ReturnType<typeof useUpdateBackupSettings>['mutate']>[0] {
  const payload: Parameters<ReturnType<typeof useUpdateBackupSettings>['mutate']>[0] = {}

  if (draft.repository !== remote.repository) payload.repository = draft.repository
  if (password) payload.password = password
  if (draft.keepDaily !== remote.keepDaily) payload.keepDaily = draft.keepDaily
  if (draft.keepWeekly !== remote.keepWeekly) payload.keepWeekly = draft.keepWeekly
  if (draft.keepMonthly !== remote.keepMonthly) payload.keepMonthly = draft.keepMonthly
  if (draft.keepYearly !== remote.keepYearly) payload.keepYearly = draft.keepYearly
  if (draft.autoPrune !== remote.autoPrune) payload.autoPrune = draft.autoPrune
  if (draft.scheduleIntervalMinutes !== remote.scheduleIntervalMinutes)
    payload.scheduleIntervalMinutes = draft.scheduleIntervalMinutes
  if (draft.syncAfterBackup !== remote.syncAfterBackup) payload.syncAfterBackup = draft.syncAfterBackup
  if (draft.rcloneRemote !== remote.rcloneRemote) payload.rcloneRemote = draft.rcloneRemote
  if (draft.rclonePath !== remote.rclonePath) payload.rclonePath = draft.rclonePath
  if (draft.rcloneTransfers !== remote.rcloneTransfers) payload.rcloneTransfers = draft.rcloneTransfers

  return payload
}

interface Draft {
  repository: string
  keepDaily: number
  keepWeekly: number
  keepMonthly: number
  keepYearly: number
  autoPrune: boolean
  scheduleIntervalMinutes: number
  syncAfterBackup: boolean
  rcloneRemote: string
  rclonePath: string
  rcloneTransfers: number
}

function toDraft(s: BackupSettings): Draft {
  return {
    repository: s.repository ?? '',
    keepDaily: s.keepDaily,
    keepWeekly: s.keepWeekly,
    keepMonthly: s.keepMonthly,
    keepYearly: s.keepYearly,
    autoPrune: s.autoPrune,
    scheduleIntervalMinutes: s.scheduleIntervalMinutes,
    syncAfterBackup: s.syncAfterBackup,
    rcloneRemote: s.rcloneRemote ?? '',
    rclonePath: s.rclonePath ?? '',
    rcloneTransfers: s.rcloneTransfers ?? 4,
  }
}

// ─── Component ────────────────────────────────────────────────────────────────

export function BackupSettingsContent() {
  const { data: settings, isLoading, isError } = useBackupSettings()
  const updateSettings = useUpdateBackupSettings()
  const initRepo = useInitRepo()
  const testCloud = useTestCloud()
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)

  const [initialized, setInitialized] = useState(false)
  const [draft, setDraft] = useState<Draft | null>(null)

  // Password field: empty = do not send; populated = update
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false)
  const [pendingReveal, setPendingReveal] = useState(false)

  // Re-mask password reveal when unlock session expires
  useEffect(() => {
    if (unlockedUntil !== null) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setShowPassword(false)
  }, [unlockedUntil])

  // Sync server state into draft once on load (not on every refetch to avoid
  // overwriting in-progress edits)
  useEffect(() => {
    if (settings && !initialized) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDraft(toDraft(settings))
      setInitialized(true)
    }
  }, [settings, initialized])

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground py-4">
        <LoadingSpinner size="small" />
        Loading backup settings…
      </div>
    )
  }

  if (isError || !settings || !draft) {
    return (
      <div className="py-4 text-sm text-destructive">
        Failed to load backup settings.
      </div>
    )
  }

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) =>
    setDraft((prev) => prev ? { ...prev, [key]: value } : prev)

  const handleSave = () => {
    const payload = buildPayload(settings, draft, password)
    if (Object.keys(payload).length === 0) {
      toast.info('No changes to save')
      return
    }
    updateSettings.mutate(payload, {
      onSuccess: () => {
        toast.success('Backup settings saved')
        setPassword('')
      },
      onError: () => toast.error('Failed to save backup settings'),
    })
  }

  const handleClearPassword = () => {
    // Send explicit empty string to revert to env fallback
    updateSettings.mutate({ password: '' }, {
      onSuccess: () => toast.success('Password cleared — reverted to environment fallback'),
      onError: () => toast.error('Failed to clear password'),
    })
  }

  const handleTogglePasswordReveal = () => {
    if (showPassword) {
      setShowPassword(false)
      return
    }
    if (authDisabled || isUnlocked()) {
      setShowPassword(true)
      return
    }
    setPendingReveal(true)
    setUnlockDialogOpen(true)
  }

  const handleUnlocked = () => {
    if (pendingReveal) setShowPassword(true)
    setPendingReveal(false)
  }

  const handleInitRepo = () => {
    initRepo.mutate(undefined, {
      onSuccess: (data) => {
        if (data.initialized) {
          toast.success('Repository initialized successfully')
        } else {
          toast.error('Repository initialization reported not-initialized')
        }
      },
      onError: () => toast.error('Failed to initialize repository'),
    })
  }

  const handleTestCloud = () => {
    testCloud.mutate(undefined, {
      onSuccess: (data) => {
        if (data.ok) {
          toast.success('Cloud connectivity test passed')
        } else {
          toast.error('Cloud connectivity test failed')
        }
      },
      onError: () => toast.error('Cloud connectivity test failed'),
    })
  }

  const isSaving = updateSettings.isPending

  // Edits live only in `draft`/`password` until saved; compare against the last
  // persisted server values to know whether anything is pending.
  const pendingChanges = buildPayload(settings, draft, password)
  const isDirty = Object.keys(pendingChanges).length > 0

  const handleDiscard = () => {
    setDraft(toDraft(settings))
    setPassword('')
  }

  return (
    <div className="space-y-6 pb-20">
      {/* Engine availability banner */}
      {(!settings.resticAvailable || !settings.rcloneAvailable) && (
        <div className="flex items-start gap-3 rounded-lg border border-destructive/30 bg-destructive/10 p-4">
          <AlertCircle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
          <div className="space-y-1">
            {!settings.resticAvailable && (
              <p className="text-sm font-medium text-destructive">
                restic binary not found — backups are disabled
              </p>
            )}
            {!settings.rcloneAvailable && (
              <p className="text-sm font-medium text-destructive">
                rclone binary not found — cloud sync is disabled
              </p>
            )}
            <p className="text-xs text-destructive/80">
              Install the missing binaries inside the container and restart Capstan.
            </p>
          </div>
        </div>
      )}

      {/* Unlock status strip */}
      <div className="flex items-center justify-end">
        <EnvUnlockStatus />
      </div>

      {/* EnvUnlock dialog (rendered once, shown on demand) */}
      <EnvUnlockDialog
        open={unlockDialogOpen}
        onOpenChange={(open) => {
          setUnlockDialogOpen(open)
          if (!open) setPendingReveal(false)
        }}
        onUnlocked={handleUnlocked}
      />

      {/* ── Status ─────────────────────────────────────────────────────────── */}
      <BackupStatusCard />

      {/* ── Repository ─────────────────────────────────────────────────────── */}
      <div className="space-y-4">
        <div className="flex items-center gap-1.5">
          <h3 className="text-lg font-medium">Repository</h3>
          <HelpHint label="restic repository" title="restic repository" side="right">
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
            value={draft.repository}
            onChange={(e) => set('repository', e.target.value)}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">
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
              onChange={(e) => setPassword(e.target.value)}
              className="flex-1"
              aria-describedby="backup-password-hint"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={handleTogglePasswordReveal}
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
              onClick={handleClearPassword}
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
            onClick={handleInitRepo}
            disabled={initRepo.isPending || !settings.resticAvailable}
            title={!settings.resticAvailable ? 'restic not available' : undefined}
          >
            {initRepo.isPending ? (
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

      {/* ── Retention ──────────────────────────────────────────────────────── */}
      <div className="space-y-4 pt-4 border-t">
        <div className="flex items-center gap-1.5">
          <h3 className="text-lg font-medium">Retention</h3>
          <HelpHint label="Retention" title="Retention" side="right">
            <p>
              After a backup, restic can thin out old snapshots, keeping a set number per day,
              week, month, and year. Set a level to 0 to keep none there.
            </p>
            <p>Snapshots are only removed when auto-prune is on or you prune by hand.</p>
          </HelpHint>
        </div>

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4 max-w-xl">
          {(
            [
              { key: 'keepDaily', label: 'Keep daily', id: 'backup-keep-daily' },
              { key: 'keepWeekly', label: 'Keep weekly', id: 'backup-keep-weekly' },
              { key: 'keepMonthly', label: 'Keep monthly', id: 'backup-keep-monthly' },
              { key: 'keepYearly', label: 'Keep yearly', id: 'backup-keep-yearly' },
            ] as const
          ).map(({ key, label, id }) => (
            <div key={key} className="space-y-1">
              <Label htmlFor={id}>{label}</Label>
              <Input
                id={id}
                type="number"
                min={0}
                value={draft[key]}
                onChange={(e) => set(key, parseInt(e.target.value, 10) || 0)}
                className="w-full"
              />
            </div>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          Number of snapshots to keep at each interval. 0 = keep none for that interval.
        </p>

        <div className="flex items-center gap-3">
          <Switch
            id="backup-auto-prune"
            checked={draft.autoPrune}
            onCheckedChange={(v) => set('autoPrune', v)}
          />
          <div>
            <Label htmlFor="backup-auto-prune">Auto-prune after backup</Label>
            <p className="text-xs text-muted-foreground">
              Automatically apply the retention policy after each backup run.
            </p>
          </div>
        </div>
      </div>

      {/* ── Schedule ───────────────────────────────────────────────────────── */}
      <div className="space-y-4 pt-4 border-t">
        <h3 className="text-lg font-medium">Schedule</h3>

        <div className="space-y-2">
          <Label htmlFor="backup-interval">Interval (minutes)</Label>
          <Input
            id="backup-interval"
            type="number"
            min={0}
            value={draft.scheduleIntervalMinutes}
            onChange={(e) =>
              set('scheduleIntervalMinutes', parseInt(e.target.value, 10) || 0)
            }
            className="max-w-xs"
          />
          <p className="text-xs text-muted-foreground">
            How often to run scheduled backups. <strong>0</strong> disables scheduled backups.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <Switch
            id="backup-sync-after"
            checked={draft.syncAfterBackup}
            onCheckedChange={(v) => set('syncAfterBackup', v)}
          />
          <div>
            <Label htmlFor="backup-sync-after">Sync to cloud after each backup</Label>
            <p className="text-xs text-muted-foreground">
              Run rclone sync automatically after every successful backup.
            </p>
          </div>
        </div>
      </div>

      {/* ── Cloud (rclone) ─────────────────────────────────────────────────── */}
      <div className="space-y-4 pt-4 border-t">
        <div className="flex items-center gap-1.5">
          <h3 className="text-lg font-medium">Cloud (rclone)</h3>
          <HelpHint label="Cloud sync" title="Cloud sync" side="right">
            <p>rclone copies the restic repository to off-site storage like S3 or Backblaze.</p>
            <p>
              &apos;Remote&apos; is the name you gave that storage in your rclone config, and
              &apos;path&apos; is the folder inside it. Test connectivity before you depend on it.
            </p>
          </HelpHint>
        </div>

        <div className="space-y-2">
          <Label htmlFor="backup-rclone-remote">Remote</Label>
          <Input
            id="backup-rclone-remote"
            type="text"
            placeholder="myremote"
            value={draft.rcloneRemote}
            onChange={(e) => set('rcloneRemote', e.target.value)}
            className="max-w-md"
          />
          <p className="text-xs text-muted-foreground">
            Name of the rclone remote as configured in your rclone config.
          </p>
        </div>

        <div className="space-y-2">
          <Label htmlFor="backup-rclone-path">Path on remote</Label>
          <Input
            id="backup-rclone-path"
            type="text"
            placeholder="bucket/backups"
            value={draft.rclonePath}
            onChange={(e) => set('rclonePath', e.target.value)}
            className="max-w-md"
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="backup-rclone-transfers">Parallel transfers</Label>
          <Input
            id="backup-rclone-transfers"
            type="number"
            min={1}
            max={32}
            value={draft.rcloneTransfers}
            onChange={(e) => set('rcloneTransfers', parseInt(e.target.value, 10) || 4)}
            className="max-w-xs"
          />
          <p className="text-xs text-muted-foreground">
            Number of parallel file transfers (rclone <code>--transfers</code>). Default: 4.
          </p>
        </div>

        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleTestCloud}
          disabled={testCloud.isPending || !settings.rcloneAvailable}
          title={!settings.rcloneAvailable ? 'rclone not available' : undefined}
        >
          {testCloud.isPending ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Testing…
            </>
          ) : (
            'Test connectivity'
          )}
        </Button>
      </div>

      {/* ── Sticky save bar ────────────────────────────────────────────────── */}
      <div className="sticky bottom-0 -mx-1 mt-2 flex items-center justify-between gap-3 border-t bg-background/95 px-1 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          {isDirty ? (
            <>
              <span className="h-2 w-2 shrink-0 rounded-full bg-amber-500" aria-hidden="true" />
              Unsaved changes
            </>
          ) : (
            'All changes saved. Fields show the last saved value; edits apply only after you Save.'
          )}
        </p>
        <div className="flex items-center gap-2">
          {isDirty && (
            <Button type="button" variant="ghost" onClick={handleDiscard} disabled={isSaving}>
              Discard
            </Button>
          )}
          <Button type="button" onClick={handleSave} disabled={isSaving || !isDirty}>
            {isSaving ? (
              <>
                <span className="mr-2"><LoadingSpinner size="small" /></span>
                Saving…
              </>
            ) : (
              'Save Backup Settings'
            )}
          </Button>
        </div>
      </div>
    </div>
  )
}
