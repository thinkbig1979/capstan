import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupSettingsContent } from '../BackupSettingsContent'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

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
  mockGetSettings.mockResolvedValue(makeSettings())
  mockUpdateSettings.mockResolvedValue(makeSettings())
  mockInitRepo.mockResolvedValue({ initialized: true })
  mockTestCloud.mockResolvedValue({ ok: true })
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

  it('shows a "No changes to save" toast when saving without any edits', async () => {
    const { toast } = await import('sonner')
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    // Wait for component to fully load.
    await screen.findByRole('button', { name: /save backup settings/i })

    // Click save without making any changes.
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => {
      expect(toast.info).toHaveBeenCalledWith('No changes to save')
    })
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
