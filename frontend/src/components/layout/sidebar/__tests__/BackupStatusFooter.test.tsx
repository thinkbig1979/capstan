import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BackupStatusFooter } from '../BackupStatusFooter'
import type { BackupRun, BackupStatus } from '@/types'

const MINUTES_AGO_5 = () => new Date(Date.now() - 5 * 60 * 1000).toISOString()

const run = (over: Partial<BackupRun> = {}): BackupRun => ({
  id: 'run-1',
  kind: 'backup',
  trigger: 'scheduled',
  status: 'success',
  startedAt: MINUTES_AGO_5(),
  finishedAt: MINUTES_AGO_5(),
  stacksTotal: 1,
  stacksOk: 1,
  stacksFailed: 0,
  bytesAdded: 0,
  ...over,
})

const status = (lastRun: BackupRun | null): BackupStatus => ({
  resticAvailable: true,
  rcloneAvailable: true,
  repoState: 'ok',
  repoStateMessage: '',
  enabledStackCount: 1,
  lastRun,
  lastVerify: null,
  nextRunAt: null,
  repoSizeBytes: null,
  schedulerRunning: true,
})

// agent-os-4zx0: lastRun is the newest run of ANY kind, and the footer read
// "Backup 5m ago" for a restore or a prune too.
describe('BackupStatusFooter — the last run line', () => {
  it.each([
    ['backup', 'Last backup 5m ago'],
    ['restore', 'Last restore 5m ago'],
    ['sync', 'Last sync 5m ago'],
    ['prune', 'Last prune 5m ago'],
    ['dr_restore', 'Last DR restore 5m ago'],
    ['verify', 'Last repository check 5m ago'],
  ] as const)('names the kind of a %s run', (kind, text) => {
    render(<BackupStatusFooter backupStatus={status(run({ kind }))} />)

    expect(screen.getByText(text)).toBeInTheDocument()
  })

  it('says no backups yet when nothing has run', () => {
    render(<BackupStatusFooter backupStatus={status(null)} />)

    expect(screen.getByText('No backups yet')).toBeInTheDocument()
  })
})
