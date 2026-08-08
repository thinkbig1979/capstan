import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupSettingsContent } from '../BackupSettingsContent'
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

    // useBackupSettings has retry:1 so allow up to 3s for the retry + error render.
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
