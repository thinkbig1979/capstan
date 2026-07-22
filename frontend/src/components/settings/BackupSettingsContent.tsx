'use client'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { BackupStatusCard } from '@/components/dashboard/BackupStatusCard'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuth } from '@/hooks/useAuth'
import { useBackupSettings } from '@/hooks/useBackup'
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
  const { data: settings, isLoading, isError } = useBackupSettings()
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

  if (isError || !settings || !draft) {
    return (
      <div className="py-4 text-sm text-destructive">
        Failed to load backup settings.
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

      <SaveBar isDirty={isDirty} isSaving={isSaving} onDiscard={handleDiscard} onSave={handleSave} />
    </div>
  )
}
