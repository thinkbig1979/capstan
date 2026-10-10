import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupSettingsContent } from '../BackupSettingsContent'
import { useAuthStore } from '@/stores/authStore'

// Sync now (agent-os-z91e.9): the pre-flight count decides whether the sync
// starts straight away or asks the operator to confirm a large remote delete,
// and a confirmation sends exactly the count it showed.

const mockGetSettings = vi.fn()
const mockSyncPreflight = vi.fn()
const mockRunSync = vi.fn()

vi.mock('@/lib/api', () => ({
  backupApi: {
    getSettings: (...args: unknown[]) => mockGetSettings(...args),
    updateSettings: vi.fn(),
    initRepo: vi.fn(),
    testCloud: vi.fn(),
    syncPreflight: (...args: unknown[]) => mockSyncPreflight(...args),
    runSync: (...args: unknown[]) => mockRunSync(...args),
    getStatus: vi.fn().mockResolvedValue({
      resticAvailable: true,
      rcloneAvailable: true,
      repoState: 'ok',
      repoStateMessage: '',
      enabledStackCount: 0,
      lastRun: null,
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    }),
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

const DIALOG_TITLE = 'Delete files from the remote?'

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ authDisabled: false })
  mockGetSettings.mockResolvedValue(settings('myremote'))
  mockRunSync.mockResolvedValue({ runId: 'run-1', wsUrl: '/ws/backups/sync/run-1' })
})

describe('BackupSettingsContent — Sync now', () => {
  it('under the cap: starts the sync without a confirmation or a count', async () => {
    mockSyncPreflight.mockResolvedValue({ remoteOnly: 5, cap: 100 })
    renderSettings()

    fireEvent.click(await screen.findByRole('button', { name: /sync now/i }))

    await waitFor(() => expect(mockRunSync).toHaveBeenCalledTimes(1))
    expect(mockRunSync.mock.calls[0][0]).toBeUndefined()
    expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument()
  })

  it('over the cap: shows the count, and confirming sends exactly that count', async () => {
    mockSyncPreflight.mockResolvedValue({ remoteOnly: 150, cap: 100 })
    renderSettings()

    fireEvent.click(await screen.findByRole('button', { name: /sync now/i }))

    expect(await screen.findByText(DIALOG_TITLE)).toBeInTheDocument()
    expect(screen.getByText(/would delete 150 files from the remote, more than the 100/)).toBeInTheDocument()
    expect(mockRunSync).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Sync and delete 150 files' }))

    await waitFor(() => expect(mockRunSync).toHaveBeenCalledTimes(1))
    expect(mockRunSync.mock.calls[0][0]).toBe(150)
    expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument()
  })

  it('over the cap, cancelled: no sync is started', async () => {
    mockSyncPreflight.mockResolvedValue({ remoteOnly: 150, cap: 100 })
    renderSettings()

    fireEvent.click(await screen.findByRole('button', { name: /sync now/i }))
    expect(await screen.findByText(DIALOG_TITLE)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByText(DIALOG_TITLE)).not.toBeInTheDocument())
    expect(mockRunSync).not.toHaveBeenCalled()
  })

  it('is disabled until a remote is saved', async () => {
    mockGetSettings.mockResolvedValue(settings(''))
    renderSettings()

    expect(await screen.findByRole('button', { name: /sync now/i })).toBeDisabled()
    expect(mockSyncPreflight).not.toHaveBeenCalled()
  })
})
