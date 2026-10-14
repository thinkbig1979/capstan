import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { BackupSettingsContent } from '../../BackupSettingsContent'
import { useAuthStore } from '@/stores/authStore'

// DR repository pull (agent-os-7n9v): a confirm step that names what the
// restore does, gated on typing the remote name, then one drRestore call.

const mockGetSettings = vi.fn()
const mockGetStatus = vi.fn()
const mockDrRestore = vi.fn()

vi.mock('@/lib/api', () => ({
  backupApi: {
    getSettings: (...args: unknown[]) => mockGetSettings(...args),
    getStatus: (...args: unknown[]) => mockGetStatus(...args),
    updateSettings: vi.fn(),
    initRepo: vi.fn(),
    testCloud: vi.fn(),
    syncPreflight: vi.fn(),
    runSync: vi.fn(),
    drRestore: (...args: unknown[]) => mockDrRestore(...args),
  },
  authApi: { verifyPassword: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

function settings(rcloneRemote: string) {
  return {
    repository: '/app/data/restic-repo',
    repositorySource: 'default' as const,
    hasPassword: true,
    passwordSource: 'db' as const,
    keepDaily: 7,
    keepWeekly: 4,
    keepMonthly: 6,
    keepYearly: 0,
    autoPrune: true,
    scheduleIntervalMinutes: 0,
    scheduleMode: 'interval' as const,
    scheduleTime: '03:00',
    scheduleDays: [0, 1, 2, 3, 4, 5, 6],
    serverTimezone: 'UTC',
    serverTimeOffset: '+00:00',
    syncAfterBackup: true,
    verifyWeekly: true,
    rcloneRemote,
    rclonePath: 'bucket/backups',
    rcloneTransfers: 4,
    hostname: 'mock-host',
    resticAvailable: true,
    rcloneAvailable: true,
    repoState: 'ok' as const,
    repoStateMessage: '',
  }
}

function status(lastRun: { status: string } | null) {
  return {
    resticAvailable: true,
    rcloneAvailable: true,
    repoState: 'ok',
    repoStateMessage: '',
    enabledStackCount: 0,
    lastRun,
    lastVerify: null,
    nextRunAt: null,
    repoSizeBytes: null,
    schedulerRunning: false,
  }
}

function renderSettings() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  render(<BackupSettingsContent />, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

const OPEN_BUTTON = /restore repository from remote/i
const DIALOG_TITLE = 'Restore the repository from the remote?'

async function openDialog() {
  fireEvent.click(await screen.findByRole('button', { name: OPEN_BUTTON }))
  return screen.findByText(DIALOG_TITLE)
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ authDisabled: false })
  mockGetSettings.mockResolvedValue(settings('myremote'))
  mockGetStatus.mockResolvedValue(status(null))
  mockDrRestore.mockResolvedValue({ runId: 'run-1', wsUrl: '/ws/backups/dr-restore/run-1' })
})

describe('BackupSettingsContent — Restore repository from remote', () => {
  it('the confirm button stays disabled until the exact remote name is typed', async () => {
    renderSettings()
    await openDialog()
    const confirm = screen.getByRole('button', { name: 'Restore repository' })
    const field = screen.getByLabelText(/^type myremote to confirm$/i, { selector: 'input' })

    expect(confirm).toBeDisabled()
    fireEvent.change(field, { target: { value: 'myremot' } })
    expect(confirm).toBeDisabled()
    fireEvent.change(field, { target: { value: 'MyRemote' } })
    expect(confirm).toBeDisabled()
    fireEvent.change(field, { target: { value: 'myremote' } })
    expect(confirm).toBeEnabled()
    expect(mockDrRestore).not.toHaveBeenCalled()
  })

  it('names the source, the local repository and the side folder, and says it restores neither database nor stacks', async () => {
    renderSettings()
    await openDialog()

    expect(screen.getByText('myremote:bucket/backups')).toBeInTheDocument()
    expect(screen.getByText('/app/data/restic-repo')).toBeInTheDocument()
    expect(screen.getByText(/\.pre-dr-/)).toBeInTheDocument()
    expect(screen.getByText(/does not restore the database or your stacks/)).toBeInTheDocument()
  })

  it('confirming calls drRestore with {confirm: true} exactly once and toasts the start', async () => {
    renderSettings()
    await openDialog()
    fireEvent.change(screen.getByLabelText(/^type myremote to confirm$/i, { selector: 'input' }), {
      target: { value: 'myremote' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Restore repository' }))

    await waitFor(() => expect(mockDrRestore).toHaveBeenCalledTimes(1))
    expect(mockDrRestore).toHaveBeenCalledWith({ confirm: true })
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Restore started', {
        description: 'The result appears in the backup history.',
      }),
    )
    expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument()
  })

  it('cancelling calls nothing', async () => {
    renderSettings()
    await openDialog()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument())
    expect(mockDrRestore).not.toHaveBeenCalled()
  })

  it('a 409 BACKUP_BUSY shows the busy error, not a success toast', async () => {
    mockDrRestore.mockRejectedValue({
      status: 409,
      code: 'BACKUP_BUSY',
      message: 'A backup operation is already in progress',
    })
    renderSettings()
    await openDialog()
    fireEvent.change(screen.getByLabelText(/^type myremote to confirm$/i, { selector: 'input' }), {
      target: { value: 'myremote' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Restore repository' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1))
    expect(toast.error).toHaveBeenCalledWith(
      'Restore could not be started',
      expect.objectContaining({ description: expect.stringContaining('already in progress') }),
    )
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('is absent when no rclone remote is configured', async () => {
    mockGetSettings.mockResolvedValue(settings(''))
    renderSettings()

    await screen.findByRole('button', { name: /sync now/i })
    expect(screen.queryByRole('button', { name: OPEN_BUTTON })).not.toBeInTheDocument()
  })

  it('is disabled while a backup operation runs', async () => {
    mockGetStatus.mockResolvedValue(status({ status: 'running' }))
    renderSettings()

    const button = await screen.findByRole('button', { name: OPEN_BUTTON })
    await waitFor(() => expect(button).toBeDisabled())
    expect(button).toHaveAttribute('title', 'A backup operation is running')
  })

  it('is disabled while the form has unsaved edits', async () => {
    renderSettings()
    const button = await screen.findByRole('button', { name: OPEN_BUTTON })
    expect(button).toBeEnabled()

    fireEvent.change(screen.getByLabelText('Path on remote'), { target: { value: 'other/path' } })

    await waitFor(() => expect(button).toBeDisabled())
  })
})
