import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CloudSection } from '../backup-settings/CloudSection'
import { buildPayload, toDraft } from '../backup-settings/backup-payload'
import type { Draft } from '../backup-settings/types'
import type { BackupSettings } from '@/types'

/**
 * agent-os-qnso: GET /settings/backup serves an rclone connection-string
 * remote masked (":s3,access_key_id=***,secret_access_key=***") with
 * rcloneRemoteHasCredential. The form says the keys are stored, hidden, the
 * way it does for a repository credential, and an untouched field is never
 * sent back.
 */
const MASKED = ':s3,access_key_id=***,secret_access_key=***'

function settings(remote: string, hasCredential: boolean): BackupSettings {
  return {
    repository: '/data/restic-repo',
    repositorySource: 'default',
    hasPassword: true,
    passwordSource: 'db',
    keepDaily: 7,
    keepWeekly: 4,
    keepMonthly: 6,
    keepYearly: 0,
    autoPrune: true,
    scheduleIntervalMinutes: 0,
    scheduleMode: 'interval',
    scheduleTime: '02:00',
    scheduleDays: [0, 1, 2, 3, 4, 5, 6],
    syncAfterBackup: false,
    verifyWeekly: true,
    rcloneRemote: remote,
    rcloneRemoteHasCredential: hasCredential,
    rclonePath: 'bucket',
    rcloneTransfers: 4,
    hostname: 'h',
    resticAvailable: true,
    rcloneAvailable: true,
  } as BackupSettings
}

function renderCloud(draft: Draft) {
  const noop = vi.fn()
  return render(
    <CloudSection
      draft={draft}
      onChange={noop}
      rcloneAvailable
      onTestCloud={noop}
      isTestingCloud={false}
      onSyncNow={noop}
      isSyncing={false}
      isDirty={false}
      pendingLargeDelete={null}
      onConfirmLargeDelete={noop}
      onLargeDeleteOpenChange={noop}
      onDrRestore={noop}
      isRestoring={false}
      backupRunning={false}
      repository="/data/restic-repo"
    />,
  )
}

describe('qnso: rclone remote credential is stored, hidden', () => {
  it('qnso: says the keys are stored, hidden for a masked remote', () => {
    renderCloud(toDraft(settings(MASKED, true)))
    expect(screen.getByText(/stored, hidden/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Remote')).toHaveAttribute('aria-describedby', expect.stringContaining('backup-rclone-remote-credential-hint'))
  })

  it('qnso: drops the hint once the operator types a new remote', () => {
    renderCloud({ ...toDraft(settings(MASKED, true)), rcloneRemote: ':s3,access_key_id=NEW,secret_access_key=NEW' })
    expect(screen.queryByText(/stored, hidden/i)).not.toBeInTheDocument()
  })

  it('qnso: shows no hint for a named remote', () => {
    renderCloud(toDraft(settings('myremote', false)))
    expect(screen.queryByText(/stored, hidden/i)).not.toBeInTheDocument()
  })

  it('qnso: never sends the untouched masked remote or the display-only fields', () => {
    const remote = settings(MASKED, true)
    expect(buildPayload(remote, toDraft(remote), '')).toEqual({})
    const edited = { ...toDraft(remote), rcloneRemote: 'myremote' }
    expect(buildPayload(remote, edited, '')).toEqual({ rcloneRemote: 'myremote' })
  })
})
