import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupSettingsContent } from '../BackupSettingsContent'
import { buildPayload, toDraft } from '../backup-settings/backup-payload'
import type { BackupSettings } from '@/types'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { toast } from 'sonner'

// ─── API mocks ───────────────────────────────────────────────────────────────

const mockGetSettings = vi.fn()
const mockUpdateSettings = vi.fn()
const mockInitRepo = vi.fn()
const mockTestCloud = vi.fn()
const mockVerifyPassword = vi.fn()

vi.mock('@/lib/api', () => ({
  backupApi: {
    getSettings: (...args: unknown[]) => mockGetSettings(...args),
    updateSettings: (...args: unknown[]) => mockUpdateSettings(...args),
    initRepo: (...args: unknown[]) => mockInitRepo(...args),
    testCloud: (...args: unknown[]) => mockTestCloud(...args),
    getStatus: vi.fn().mockResolvedValue({
      resticAvailable: true,
      rcloneAvailable: true,
      repositoryInitialized: false,
      enabledStackCount: 0,
      lastRun: null,
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    }),
  },
  authApi: {
    verifyPassword: (...args: unknown[]) => mockVerifyPassword(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// ─── Helpers ─────────────────────────────────────────────────────────────────

function makeSettings(overrides: Partial<{
  hasPassword: boolean
  passwordSource: 'env' | 'db' | 'default'
  repositorySource: 'env' | 'db' | 'default'
  repository: string
  resticAvailable: boolean
  rcloneAvailable: boolean
  repositoryInitialized: boolean
  scheduleIntervalMinutes: number
  scheduleMode: 'interval' | 'scheduled'
  scheduleTime: string
  scheduleDays: number[]
  serverTimezone: string
  serverTimeOffset: string
}> = {}) {
  return {
    repository: '/app/data/restic-repo',
    repositorySource: 'default' as const,
    hasPassword: false,
    passwordSource: 'default' as const,
    keepDaily: 7,
    keepWeekly: 4,
    keepMonthly: 6,
    keepYearly: 0,
    autoPrune: true,
    scheduleIntervalMinutes: 0,
    scheduleMode: 'interval' as const,
    scheduleTime: '03:00',
    // Ascending Go weekday ints, 0 = Sunday. Every day, the server default.
    scheduleDays: [0, 1, 2, 3, 4, 5, 6],
    // A default install sets no TZ, so the backend reports UTC.
    serverTimezone: 'UTC',
    serverTimeOffset: '+00:00',
    syncAfterBackup: false,
    rcloneRemote: '',
    rclonePath: '',
    rcloneTransfers: 4,
    hostname: 'mock-host',
    resticAvailable: true,
    rcloneAvailable: true,
    repositoryInitialized: false,
    ...overrides,
  }
}

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  useEnvUnlockStore.getState().lock()
  useAuthStore.setState({ authDisabled: false })
  mockGetSettings.mockResolvedValue(makeSettings())
  mockUpdateSettings.mockResolvedValue(makeSettings())
  mockInitRepo.mockResolvedValue({ initialized: true })
  mockTestCloud.mockResolvedValue({ ok: true })
})

afterEach(() => {
  act(() => {
    useEnvUnlockStore.getState().lock()
  })
  useAuthStore.setState({ authDisabled: false })
})

// ─── Tests ───────────────────────────────────────────────────────────────────

describe('BackupSettingsContent — loading / error states', () => {
  it('renders a loading spinner while settings are fetching', async () => {
    mockGetSettings.mockImplementation(
      () => new Promise(() => {}), // never resolves
    )
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    expect(screen.getByText(/loading backup settings/i)).toBeInTheDocument()
  })

  it('renders an error message when settings fail to load', async () => {
    mockGetSettings.mockRejectedValue(new Error('Network error'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    // useBackupSettings no longer restates `retry: 1` over the wrapper's
    // `retry: false` (agent-os-tdts), so this settles on the first failure. The
    // timeout is headroom for a loaded box, not a retry backoff.
    await waitFor(
      () => {
        expect(screen.getByText(/failed to load backup settings/i)).toBeInTheDocument()
      },
      { timeout: 3000 },
    )
  })
})

describe('BackupSettingsContent — renders effective settings', () => {
  it('renders the repository path from settings', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ repository: '/mnt/backups/restic' }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByDisplayValue('/mnt/backups/restic')).toBeInTheDocument()
    })
  })

  it('renders the correct keep-daily value', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      const keepDailyInput = screen.getByLabelText('Keep daily') as HTMLInputElement
      expect(keepDailyInput.value).toBe('7')
    })
  })

  it('renders "Not initialized" status when repository is not initialized', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Not initialized')).toBeInTheDocument()
    })
  })

  it('renders "Initialized" status when repository is initialized', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ repositoryInitialized: true }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Initialized')).toBeInTheDocument()
    })
  })

  it('shows engine unavailability banner when restic is missing', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ resticAvailable: false }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/restic binary not found/i)).toBeInTheDocument()
    })
  })

  it('shows engine unavailability banner when rclone is missing', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ rcloneAvailable: false }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/rclone binary not found/i)).toBeInTheDocument()
    })
  })
})

describe('BackupSettingsContent — password field security', () => {
  it('does not display existing password value — input starts empty', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ hasPassword: true }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      const passwordInput = screen.getByLabelText(/repository password/i) as HTMLInputElement
      expect(passwordInput.value).toBe('')
    })
  })

  it('shows "(currently set)" hint when hasPassword is true', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ hasPassword: true }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/currently set/i)).toBeInTheDocument()
    })
  })

  it('password input is type="password" by default (masked)', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      const passwordInput = screen.getByLabelText(/repository password/i) as HTMLInputElement
      expect(passwordInput.type).toBe('password')
    })
  })

  it('shows "Clear saved password" button when hasPassword=true and passwordSource=db', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({
      hasPassword: true,
      passwordSource: 'db',
    }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Clear saved password')).toBeInTheDocument()
    })
  })

  it('does not show "Clear saved password" when passwordSource is not db', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({
      hasPassword: true,
      passwordSource: 'env',
    }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      // Ensure the component rendered.
      expect(screen.getByLabelText(/repository password/i)).toBeInTheDocument()
    })
    expect(screen.queryByText('Clear saved password')).not.toBeInTheDocument()
  })
})

describe('BackupSettingsContent — editing and saving', () => {
  it('calls updateSettings with changed repository path on save', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const repoInput = await screen.findByLabelText('Repository path')
    fireEvent.change(repoInput, { target: { value: '/new/path' } })

    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({ repository: '/new/path' }),
      )
    })
  })

  it('calls updateSettings with new password when password field is filled', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const passwordInput = await screen.findByLabelText(/repository password/i)
    fireEvent.change(passwordInput, { target: { value: 'my-new-password' } })

    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({ password: 'my-new-password' }),
      )
    })
  })

  it('disables Save and shows "All changes saved" when there are no edits', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    // Wait for component to fully load.
    const saveButton = await screen.findByRole('button', { name: /save backup settings/i })

    // With no pending edits the sticky bar reports a clean state and Save is disabled.
    expect(saveButton).toBeDisabled()
    expect(screen.getByText(/all changes saved/i)).toBeInTheDocument()
  })

  it('calls updateSettings with empty password string when "Clear saved password" is clicked', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ hasPassword: true, passwordSource: 'db' }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const clearBtn = await screen.findByText('Clear saved password')
    fireEvent.click(clearBtn)

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith({ password: '' })
    })
  })
})

describe('BackupSettingsContent — repository initialization', () => {
  it('calls initRepo when "Initialize repository" button is clicked', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const initBtn = await screen.findByRole('button', { name: /initialize repository/i })
    fireEvent.click(initBtn)

    await waitFor(() => {
      expect(mockInitRepo).toHaveBeenCalled()
    })
  })

  it('disables "Initialize repository" button when restic is not available', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ resticAvailable: false }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const initBtn = await screen.findByRole('button', { name: /initialize repository/i })
    expect(initBtn).toBeDisabled()
  })
})

describe('BackupSettingsContent — cloud connectivity test', () => {
  it('calls testCloud when "Test connectivity" button is clicked', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const testBtn = await screen.findByRole('button', { name: /test connectivity/i })
    fireEvent.click(testBtn)

    await waitFor(() => {
      expect(mockTestCloud).toHaveBeenCalled()
    })
  })

  it('disables "Test connectivity" button when rclone is not available', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ rcloneAvailable: false }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const testBtn = await screen.findByRole('button', { name: /test connectivity/i })
    expect(testBtn).toBeDisabled()
  })
})

describe('BackupSettingsContent — password reveal / unlock flow', () => {
  it('reveal on a locked, auth-enabled password opens the unlock dialog instead of revealing it', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const revealBtn = await screen.findByRole('button', { name: 'Reveal backup password' })
    fireEvent.click(revealBtn)

    expect(await screen.findByText('Unlock environment variables')).toBeInTheDocument()
    const passwordInput = screen.getByLabelText(/repository password/i) as HTMLInputElement
    expect(passwordInput.type).toBe('password')
  })

  it('Cancel closes the dialog without revealing the password or unlocking the session', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: 'Reveal backup password' }))
    await screen.findByText('Unlock environment variables')

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByText('Unlock environment variables')).not.toBeInTheDocument()
    })
    const passwordInput = screen.getByLabelText(/repository password/i) as HTMLInputElement
    expect(passwordInput.type).toBe('password')
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(false)
  })

  it('a correct password unlocks the session and reveals the password', async () => {
    mockVerifyPassword.mockResolvedValue({ ok: true, unlockToken: 'test-unlock-token', expiresIn: 300 })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: 'Reveal backup password' }))
    await screen.findByText('Unlock environment variables')

    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'correct-password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Unlock' }))

    await waitFor(() => {
      const passwordInput = screen.getByLabelText(/repository password/i) as HTMLInputElement
      expect(passwordInput.type).toBe('text')
    })
    expect(mockVerifyPassword).toHaveBeenCalledWith('correct-password')
    expect(useEnvUnlockStore.getState().isUnlocked()).toBe(true)
  })

  it('with auth disabled, reveal bypasses the dialog entirely', async () => {
    useAuthStore.setState({ authDisabled: true })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: 'Reveal backup password' }))

    await waitFor(() => {
      const passwordInput = screen.getByLabelText(/repository password/i) as HTMLInputElement
      expect(passwordInput.type).toBe('text')
    })
    expect(screen.queryByText('Unlock environment variables')).not.toBeInTheDocument()
  })

  it('toggling reveal off hides the password immediately regardless of lock state', async () => {
    useAuthStore.setState({ authDisabled: true })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: 'Reveal backup password' }))
    await waitFor(() => {
      expect((screen.getByLabelText(/repository password/i) as HTMLInputElement).type).toBe('text')
    })

    fireEvent.click(screen.getByRole('button', { name: 'Hide backup password' }))
    expect((screen.getByLabelText(/repository password/i) as HTMLInputElement).type).toBe('password')
  })

  it('re-masks the revealed password when the unlock session expires (manual lock)', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await screen.findByRole('button', { name: 'Reveal backup password' })
    act(() => {
      useEnvUnlockStore.getState().unlock()
    })
    // Unlocked: reveal proceeds directly without the dialog.
    fireEvent.click(screen.getByRole('button', { name: 'Reveal backup password' }))
    await waitFor(() => {
      expect((screen.getByLabelText(/repository password/i) as HTMLInputElement).type).toBe('text')
    })

    act(() => {
      useEnvUnlockStore.getState().lock()
    })

    await waitFor(() => {
      expect((screen.getByLabelText(/repository password/i) as HTMLInputElement).type).toBe('password')
    })
  })
})

describe('BackupSettingsContent — draft editing and discard', () => {
  it('editing a retention field marks the form dirty, enables Save, and is included in the payload', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const keepWeeklyInput = await screen.findByLabelText('Keep weekly')
    fireEvent.change(keepWeeklyInput, { target: { value: '10' } })

    const saveButton = screen.getByRole('button', { name: /save backup settings/i })
    expect(saveButton).not.toBeDisabled()
    expect(screen.getByText(/unsaved changes/i)).toBeInTheDocument()

    fireEvent.click(saveButton)
    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(expect.objectContaining({ keepWeekly: 10 }))
    })
  })

  it('includes edited rclone, schedule, and sync-after-backup fields in the save payload', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.change(await screen.findByLabelText('Remote'), { target: { value: 'myremote' } })
    fireEvent.change(screen.getByLabelText('Path on remote'), { target: { value: 'bucket/path' } })
    fireEvent.change(screen.getByLabelText('Parallel transfers'), { target: { value: '8' } })
    fireEvent.change(screen.getByLabelText('Interval (minutes)'), { target: { value: '30' } })
    fireEvent.click(screen.getByRole('switch', { name: /sync to cloud after each backup/i }))

    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          rcloneRemote: 'myremote',
          rclonePath: 'bucket/path',
          rcloneTransfers: 8,
          scheduleIntervalMinutes: 30,
          syncAfterBackup: true,
        }),
      )
    })
  })

  it('Discard reverts an edited field and re-hides the unsaved-changes indicator', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const repoInput = (await screen.findByLabelText('Repository path')) as HTMLInputElement
    fireEvent.change(repoInput, { target: { value: '/new/path' } })
    expect(screen.getByText(/unsaved changes/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    expect(repoInput.value).toBe('/app/data/restic-repo')
    expect(screen.getByText(/all changes saved/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save backup settings/i })).toBeDisabled()
  })

  it('Discard clears an in-progress password edit', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const passwordInput = (await screen.findByLabelText(/repository password/i)) as HTMLInputElement
    fireEvent.change(passwordInput, { target: { value: 'temp-pass' } })
    expect(screen.getByText(/unsaved changes/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    expect(passwordInput.value).toBe('')
    expect(screen.getByText(/all changes saved/i)).toBeInTheDocument()
  })
})

describe('BackupSettingsContent — error handling', () => {
  it('shows an error toast when saving settings fails', async () => {
    mockUpdateSettings.mockRejectedValue(new Error('fail'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const repoInput = await screen.findByLabelText('Repository path')
    fireEvent.change(repoInput, { target: { value: '/new/path' } })
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Failed to save backup settings')
    })
  })

  it('shows an error toast when the clear-password request fails', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ hasPassword: true, passwordSource: 'db' }))
    mockUpdateSettings.mockRejectedValue(new Error('fail'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByText('Clear saved password'))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Failed to clear password')
    })
  })

  it('shows an error toast when the repository initialization request fails', async () => {
    mockInitRepo.mockRejectedValue(new Error('fail'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Failed to initialize repository')
    })
  })

  it('shows an error toast when initRepo succeeds but reports not-initialized', async () => {
    mockInitRepo.mockResolvedValue({ initialized: false })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Repository initialization reported not-initialized')
    })
  })

  it('shows an error toast when the cloud test request fails', async () => {
    mockTestCloud.mockRejectedValue(new Error('fail'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Cloud connectivity test failed')
    })
  })

  it('shows an error toast when testCloud succeeds but reports not ok', async () => {
    mockTestCloud.mockResolvedValue({ ok: false })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Cloud connectivity test failed')
    })
  })
})

describe('BackupSettingsContent — source badges', () => {
  it('shows "from environment" badge when repositorySource is env', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ repositorySource: 'env' }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('from environment')).toBeInTheDocument()
    })
  })

  it('shows "saved" badge when passwordSource is db', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ passwordSource: 'db' }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    await waitFor(() => {
      expect(screen.getAllByText('saved').length).toBeGreaterThan(0)
    })
  })
})

describe('BackupSettingsContent — fixed-time schedule', () => {
  /** Switch the Schedule section to "At a set time" and wait for the fields to appear. */
  async function switchToScheduledMode() {
    fireEvent.click(await screen.findByRole('radio', { name: 'At a set time' }))
    return screen.findByLabelText('Time of day')
  }

  it('shows the interval field in interval mode and the time field in scheduled mode', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    // Interval mode is the default, so the interval input is the visible control.
    expect(await screen.findByLabelText('Interval (minutes)')).toBeInTheDocument()
    expect(screen.queryByLabelText('Time of day')).not.toBeInTheDocument()

    await switchToScheduledMode()

    expect(screen.queryByLabelText('Interval (minutes)')).not.toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Days' })).toBeInTheDocument()
  })

  it('reports the server clock with the values a default install produces', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    expect(
      await screen.findByText("Times are in UTC (+00:00), the server's own clock."),
    ).toBeInTheDocument()
  })

  it('sends scheduleMode, scheduleTime and scheduleDays when saving in scheduled mode', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const timeInput = await switchToScheduledMode()
    fireEvent.change(timeInput, { target: { value: '02:30' } })
    // Drop Sunday (Go weekday 0); the component re-emits the rest ascending.
    fireEvent.click(screen.getByRole('button', { name: 'Sunday' }))

    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          scheduleMode: 'scheduled',
          scheduleTime: '02:30',
          scheduleDays: [1, 2, 3, 4, 5, 6],
        }),
      )
    })
  })

  it('still sends scheduleIntervalMinutes when saving in interval mode', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.change(await screen.findByLabelText('Interval (minutes)'), {
      target: { value: '45' },
    })
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalled()
    })
    const payload = mockUpdateSettings.mock.calls[0][0]
    expect(payload).toHaveProperty('scheduleIntervalMinutes', 45)
    // Mode was never touched, so it must not ride along.
    expect(payload).not.toHaveProperty('scheduleMode')
  })

  it('omits an untouched scheduleDays but includes a genuinely changed one', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    // Case that must be OMITTED: mode changes, the day set does not. scheduleDays
    // is an array, so a reference compare would wrongly mark it dirty here.
    await switchToScheduledMode()
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledTimes(1)
    })
    const unchanged = mockUpdateSettings.mock.calls[0][0]
    expect(unchanged).toHaveProperty('scheduleMode', 'scheduled')
    expect(unchanged).not.toHaveProperty('scheduleDays')

    // Case that must be INCLUDED, on the same instrument: toggle a day off.
    fireEvent.click(screen.getByRole('button', { name: 'Wednesday' }))
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledTimes(2)
    })
    const changed = mockUpdateSettings.mock.calls[1][0]
    expect(changed).toHaveProperty('scheduleDays', [0, 1, 2, 4, 5, 6])
  })

  it('keeps Save disabled when the schedule fields are only rendered, never edited', async () => {
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const saveButton = await screen.findByRole('button', { name: /save backup settings/i })
    expect(screen.getByRole('radio', { name: 'Every so often' })).toBeInTheDocument()
    expect(saveButton).toBeDisabled()
  })

  it('refuses to empty the day set, so a scheduled backup always has a day to fire on', async () => {
    mockGetSettings.mockResolvedValue(makeSettings({ scheduleMode: 'scheduled', scheduleDays: [3] }))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    const wednesday = await screen.findByRole('button', { name: 'Wednesday' })
    expect(wednesday).toHaveAttribute('aria-pressed', 'true')

    fireEvent.click(wednesday)

    expect(wednesday).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /save backup settings/i })).toBeDisabled()
  })
})

describe('buildPayload — scheduleDays is compared by value, not by reference', () => {
  /**
   * The UI-level tests above cannot see this bug: toDraft passes the settings
   * array through by reference, so draft and remote share one object and even a
   * reference compare looks correct. The identity only diverges when the
   * settings query refetches and parses a fresh, equal array — which is every
   * refetch. A reference compare pins the form dirty from that moment on,
   * because isDirty is derived from this payload (useBackupForm.ts:39-40).
   */
  it('treats an equal-but-distinct array from a refetch as unchanged', () => {
    const remote = makeSettings() as BackupSettings
    const draft = toDraft(remote)
    const refetched: BackupSettings = { ...remote, scheduleDays: [...remote.scheduleDays] }

    expect(refetched.scheduleDays).not.toBe(draft.scheduleDays)
    expect(buildPayload(refetched, draft, '')).toEqual({})
  })

  it('sends a genuinely changed day set', () => {
    const remote = makeSettings() as BackupSettings
    const draft = { ...toDraft(remote), scheduleDays: [1, 3, 5] }

    expect(buildPayload(remote, draft, '')).toEqual({ scheduleDays: [1, 3, 5] })
  })

  it('stays clean when the server omits scheduleDays and the default fills in', () => {
    const remote = makeSettings() as BackupSettings
    // A backend that predates the schedule fields sends nothing for them.
    delete (remote as Partial<BackupSettings>).scheduleDays
    const draft = toDraft(remote)

    // The default is a fresh array on every call, so a reference compare would
    // report this untouched form as dirty forever.
    expect(buildPayload(remote, draft, '')).toEqual({})
  })
})
