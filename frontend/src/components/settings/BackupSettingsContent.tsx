'use client'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { BackupStatusCard } from '@/components/dashboard/BackupStatusCard'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuth } from '@/hooks/useAuth'
import { useBackupSettings } from '@/hooks/useBackup'
import { classifyError } from '@/lib/error-handler'
import { RefreshFailedNotice } from '@/components/RefreshFailedNotice'
import { CloudSection } from './backup-settings/CloudSection'
import { EngineAvailabilityBanner } from './backup-settings/EngineAvailabilityBanner'
import { RepositorySection } from './backup-settings/RepositorySection'
import { RetentionSection } from './backup-settings/RetentionSection'
import { SaveBar } from './backup-settings/SaveBar'
import { ScheduleSection } from './backup-settings/ScheduleSection'
import { useBackupActions } from './backup-settings/useBackupActions'
import { useBackupForm } from './backup-settings/useBackupForm'
import { usePasswordReveal } from './backup-settings/usePasswordReveal'

export function BackupSettingsContent() {
  const { data: settings, isLoading, isError, error, refetch } = useBackupSettings()
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)

  const {
    draft,
    set,
    password,
    setPassword,
    isDirty,
    isSaving,
    handleSave,
    handleDiscard,
    handleClearPassword,
  } = useBackupForm(settings)

  const {
    showPassword,
    unlockDialogOpen,
    handleTogglePasswordReveal,
    handleUnlocked,
    handleDialogOpenChange,
  } = usePasswordReveal(authDisabled, isUnlocked, unlockedUntil)

  const { handleInitRepo, isInitializing, handleTestCloud, isTestingCloud } = useBackupActions()

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground py-4">
        <LoadingSpinner size="small" />
        Loading backup settings…
      </div>
    )
  }

  // agent-os-wczm: the `isError` disjunct is gone. `!settings` already covers
  // "failed and never loaded", so this branch still fires for a first-load
  // failure — but an error over settings we ALREADY have now falls through to
  // the populated form instead of blanking it, and a Save here is a WRITE-BACK,
  // so blanking it also threw away whatever the operator had typed.
  if (!settings || !draft) {
    // agent-os-rtn8: getSettings emits ONLY a 200 and three 500s, and
    // backup.go:266-270 states the distinctness is DELIBERATE -- "all three
    // refuse the same request, and an operator reading the log needs to know
    // WHICH read failed". The backend did that work and this screen threw it
    // away. (It reaches us at all only because agent-os-mc4i stopped
    // classifyError's 5xx arm discarding the message.)
    //
    // Gated on isError, NOT on the whole condition: two states route into this
    // branch and only the first carries an error. A resolved payload that is
    // merely empty has no cause, and manufacturing one would be the worse
    // defect. The fixed sentence stays as the headline in every case.
    const cause = isError ? classifyError(error).message : null
    return (
      <div className="py-4 text-sm text-destructive">
        <p>Failed to load backup settings.</p>
        {cause && <p className="mt-1">{cause}</p>}
      </div>
    )
  }

  return (
    <div className="space-y-6 pb-20">
      <EngineAvailabilityBanner
        resticAvailable={settings.resticAvailable}
        rcloneAvailable={settings.rcloneAvailable}
      />

      {/* Unlock status strip */}
      <div className="flex items-center justify-end">
        <EnvUnlockStatus />
      </div>

      {/* EnvUnlock dialog (rendered once, shown on demand) */}
      <EnvUnlockDialog
        open={unlockDialogOpen}
        onOpenChange={handleDialogOpenChange}
        onUnlocked={handleUnlocked}
      />

      {/* ── Status ─────────────────────────────────────────────────────────── */}
      <BackupStatusCard />

      <RepositorySection
        settings={settings}
        repository={draft.repository}
        onRepositoryChange={(value) => set('repository', value)}
        password={password}
        onPasswordChange={setPassword}
        showPassword={showPassword}
        onTogglePasswordReveal={handleTogglePasswordReveal}
        onClearPassword={handleClearPassword}
        isSaving={isSaving}
        onInitRepo={handleInitRepo}
        isInitializing={isInitializing}
      />

      <RetentionSection draft={draft} onChange={set} />

      <ScheduleSection draft={draft} onChange={set} />

      <CloudSection
        draft={draft}
        onChange={set}
        rcloneAvailable={settings.rcloneAvailable}
        onTestCloud={handleTestCloud}
        isTestingCloud={isTestingCloud}
      />

      {/* Directly above the save control, not a transient toast: the operator is
          about to write these fields back and has to know the form may be stale
          (agent-os-wczm). */}
      {isError && (
        <RefreshFailedNotice
          what="the backup settings"
          beforeSave
          onRetry={() => refetch()}
        />
      )}

      <SaveBar isDirty={isDirty} isSaving={isSaving} onDiscard={handleDiscard} onSave={handleSave} />
    </div>
  )
}
