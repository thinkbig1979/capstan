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
      repoState: 'uninitialized',
      repoStateMessage: '',
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
  repoState: BackupSettings['repoState']
  repoStateMessage: string
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
    repoState: 'uninitialized' as const,
    repoStateMessage: '',
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

  // agent-os-rtn8: getSettings (backup.go:148-338) emits ONLY a 200 and three
  // 500s, and backup.go:266-270 says the distinctness is deliberate -- "all
  // three refuse the same request, and an operator reading the log needs to
  // know WHICH read failed". The screen said "Failed to load backup settings."
  // (The cause only reaches here because agent-os-mc4i stopped classifyError's
  // 5xx arm discarding it; before that this test could not have passed.)
  describe('the error state shows which read failed (agent-os-rtn8)', () => {
    it('renders the backend cause alongside the fixed sentence', async () => {
      mockGetSettings.mockRejectedValue({
        status: 500,
        code: 'INTERNAL_ERROR',
        message: 'Failed to read the backup retention and schedule settings',
      })
      render(<BackupSettingsContent />, { wrapper: createWrapper() })

      await waitFor(
        () => {
          expect(screen.getByText(/failed to load backup settings/i)).toBeInTheDocument()
        },
        { timeout: 3000 },
      )
      expect(
        screen.getByText(/Failed to read the backup retention and schedule settings/),
      ).toBeInTheDocument()
    })

    // The backend's three call sites carry TWO distinct strings. An assertion on
    // one alone passes against a hardcoded string, so pin that they differ.
    it('tells the two distinct reads apart', async () => {
      mockGetSettings.mockRejectedValue({
        status: 500,
        code: 'INTERNAL_ERROR',
        message: 'Failed to read backup settings',
      })
      render(<BackupSettingsContent />, { wrapper: createWrapper() })

      await waitFor(
        () => {
          expect(screen.getByText(/500: Failed to read backup settings/)).toBeInTheDocument()
        },
        { timeout: 3000 },
      )
      expect(
        screen.queryByText(/retention and schedule/),
      ).not.toBeInTheDocument()
    })

    // TWO-SIDED: a failure carrying no cause still shows the fixed sentence
    // alone. Green before the change and must stay green.
    it('shows only the fixed sentence when the failure carries no cause', async () => {
      mockGetSettings.mockRejectedValue({ status: 500 })
      render(<BackupSettingsContent />, { wrapper: createWrapper() })

      await waitFor(
        () => {
          expect(screen.getByText(/failed to load backup settings/i)).toBeInTheDocument()
        },
        { timeout: 3000 },
      )
      expect(screen.queryByText(/Failed to read/)).not.toBeInTheDocument()
    })

    // THE TRAP AT THIS CALL SITE: `isError || !settings || !draft` routes THREE
    // conditions into one branch, and only the first has an error to read.
    // A resolved-but-empty payload must not be given a cause -- there is none,
    // and inventing one is the worse defect.
    it('does not claim a cause when the query succeeded but carried no settings', async () => {
      mockGetSettings.mockResolvedValue(undefined)
      render(<BackupSettingsContent />, { wrapper: createWrapper() })

      await waitFor(
        () => {
          expect(screen.getByText(/failed to load backup settings/i)).toBeInTheDocument()
        },
        { timeout: 3000 },
      )
      expect(screen.queryByText(/^5\d\d:/)).not.toBeInTheDocument()
    })
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
    mockGetSettings.mockResolvedValue(makeSettings({ repoState: 'ok' }))
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
      expect(toast.error).toHaveBeenCalledWith('Failed to clear password', { description: 'fail' })
    })
  })

  it('shows an error toast when the repository initialization request fails', async () => {
    // Criterion 3's NEGATIVE arm for this site, and it predates agent-os-nhiv:
    // a bare Error carries no `code`, so repoFaultFrom declines it and the
    // generic sentence must survive. Making every failure a repository story
    // would be worse than the bug this file's next two cases pin.
    mockInitRepo.mockRejectedValue(new Error('fail'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Failed to initialize repository')
    })
  })

  it('names an unreachable repository instead of "Failed to initialize repository"', async () => {
    // agent-os-81vr stopped the server CREATING a repository over an
    // unreachable one. It did not fix what the operator is then told, so the
    // retry loop it set out to break survived in the toast.
    mockInitRepo.mockRejectedValue({
      code: 'BACKUP_REPO_UNREACHABLE',
      message: 'backup repository did not answer',
      details: { repoState: 'unreachable' },
      status: 503,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('The backup repository could not be reached.', {
        description: 'backup repository did not answer',
      })
    })
    expect(toast.error).not.toHaveBeenCalledWith('Failed to initialize repository')
  })

  it('names a rejected restic password rather than a generic init failure', async () => {
    mockInitRepo.mockRejectedValue({
      code: 'BACKUP_REPO_UNREACHABLE',
      message: 'restic rejected the configured password',
      details: { repoState: 'wrong_password' },
      status: 503,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('The restic password was rejected.', {
        description: 'restic rejected the configured password',
      })
    })
  })

  it('names a missing backup engine, which this endpoint answers with its own code', async () => {
    // repoInit's `!av.ResticPresent` guard returns BEFORE CheckRepository, so
    // this site emits BACKUP_UNAVAILABLE as well as the 503 — which is why the
    // helper is reused whole rather than narrowed to one code here.
    mockInitRepo.mockRejectedValue({
      code: 'BACKUP_UNAVAILABLE',
      message: 'restic binary not found',
      details: { cause: 'restic_missing' },
      status: 409,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('The backup engine is not available.', {
        description: 'restic binary not found',
      })
    })
  })

  it('states the resulting state on success instead of claiming work happened', async () => {
    // The success body is gin.H{"initialized": true} at BOTH of repoInit's 200
    // returns, and the FIRST fires on RepoStateOK having created nothing. So
    // "Repository initialized successfully" was false exactly half the time,
    // for an operator who cannot tell which half they got.
    mockInitRepo.mockResolvedValue({ initialized: true })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /initialize repository/i }))

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('Backup repository is ready')
    })
    expect(toast.success).not.toHaveBeenCalledWith('Repository initialized successfully')
  })

  it('shows an error toast when the cloud test request fails', async () => {
    // The NEGATIVE arm for the onError route, and it must survive agent-os-3wyv.
    // A bare Error carries no `code`, so neither repoFaultFrom nor the
    // VALIDATION_ERROR read below it accepts this, and the generic sentence is
    // all there is to say. This arm has a real producer: the axios interceptor's
    // no-response branch (lib/api.ts) fires on a transport failure, and a 500
    // carries no code either. Making every cloud-test failure a named story
    // would be the fabrication this bead exists to stop.
    mockTestCloud.mockRejectedValue(new Error('fail'))
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Cloud connectivity test failed')
    })
  })

  it('names a missing rclone binary rather than "Cloud connectivity test failed"', async () => {
    // cloudTest guards on `!av.RclonePresent` and answers 409 BACKUP_UNAVAILABLE,
    // so the cause on the wire is `rclone_missing` — NOT the `restic_missing`
    // every other consumer of repoFaultFrom sends. Routing this site through the
    // helper before it grew a cause arm would have told an operator whose rclone
    // is absent that their restic is missing, green under both gates.
    mockTestCloud.mockRejectedValue({
      code: 'BACKUP_UNAVAILABLE',
      message: 'rclone binary not found in PATH',
      details: { cause: 'rclone_missing' },
      status: 409,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('The cloud sync engine is not available.', {
        description: 'rclone binary not found in PATH',
      })
    })
    expect(toast.error).not.toHaveBeenCalledWith('Cloud connectivity test failed')
    // The fabrication this arm exists to prevent, named explicitly: the restic
    // copy is what repoFaultFrom answered for EVERY BACKUP_UNAVAILABLE before
    // the cause arm, and it is wrong here in the one way that matters.
    expect(toast.error).not.toHaveBeenCalledWith(
      'The backup engine is not available.',
      expect.anything(),
    )
  })

  it('names an unconfigured rclone remote, which this endpoint answers with VALIDATION_ERROR', async () => {
    // The 400 half of the onError route. repoFaultFrom declines this code by
    // design and must keep declining it: previewSnapshot mints VALIDATION_ERROR
    // too, so a VALIDATION arm in the shared helper would change a different
    // consumer's rendering. This site reads the server's own message itself.
    mockTestCloud.mockRejectedValue({
      code: 'VALIDATION_ERROR',
      message: 'rclone remote is not configured',
      status: 400,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('rclone remote is not configured')
    })
    expect(toast.error).not.toHaveBeenCalledWith('Cloud connectivity test failed')
  })

  // agent-os-06c1: validationMessage declares `string | null` and reaches it
  // through `error as { code?: string; message?: string }`. Its result becomes
  // the toast TITLE, so a non-string would be rendered as the whole message.
  it('falls through to the generic sentence when VALIDATION_ERROR carries a non-string message', async () => {
    mockTestCloud.mockRejectedValue({
      code: 'VALIDATION_ERROR',
      message: { remote: 'offsite' },
      status: 400,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Cloud connectivity test failed')
    })
  })

  it('names why the connectivity test failed when the server answers 200 with ok:false', async () => {
    // This case used to mock `{ ok: false }` with NO error field and assert the
    // generic sentence — a body backup.go's cloudTest CANNOT SEND. Its single
    // ok:false emitter always carries `"error": err.Error()`, and both error
    // returns inside rclone's TestConnectivity are fmt.Errorf with non-empty
    // literals. So the old case pinned a dead branch, which is exactly the
    // defect handleInitRepo's comment records this wave as having just deleted.
    // Rewritten to the shape the server actually sends rather than deleted,
    // because this route is the one SITE B discards and it needs an arm.
    mockTestCloud.mockResolvedValue({
      ok: false,
      error: 'rclone: directory not found: remote "offsite" does not exist',
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /test connectivity/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith(
        'rclone: directory not found: remote "offsite" does not exist',
      )
    })
    expect(toast.error).not.toHaveBeenCalledWith('Cloud connectivity test failed')
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

/**
 * agent-os-zlw0. useBackupForm's save onError took no parameter. The backup
 * updateSettings handler (backend/internal/handlers/backup.go:402) answers with
 * VALIDATION_ERROR for a bad body and for every schedule field, and — unusually
 * — at 422 rather than 400 for the two repository refusals at backup.go:449 and
 * :462, plus ENCRYPTION_KEY_MISSING (422) from its password write at :475. The
 * two 422 VALIDATION_ERRORs are why the cause reader keys on the CODE: a
 * status-keyed one looking for 400 would drop the two most actionable sentences
 * on this endpoint.
 */
describe('BackupSettingsContent — why the save failed', () => {
  it('names what the server rejected', async () => {
    mockUpdateSettings.mockRejectedValue({
      error: 'Unprocessable Entity',
      code: 'VALIDATION_ERROR',
      message:
        'The repository value still contains the redacted credential marker (***). Re-enter the full repository URI including its credentials, or leave the field unchanged.',
      status: 422,
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.change(await screen.findByLabelText('Repository path'), {
      target: { value: 'rest:https://***@host/repo/' },
    })
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to save backup settings', {
        description: expect.stringContaining('redacted credential marker'),
      }),
    )
  })

  // MUST-PASS side, same instrument.
  it('shows the generic sentence alone when the failure carries no server cause', async () => {
    mockUpdateSettings.mockRejectedValue({
      error: 'Unknown error',
      code: 'ERR_NETWORK',
      message: 'Network Error',
    })
    const wrapper = createWrapper()
    render(<BackupSettingsContent />, { wrapper })

    fireEvent.change(await screen.findByLabelText('Repository path'), {
      target: { value: '/new/path' },
    })
    fireEvent.click(screen.getByRole('button', { name: /save backup settings/i }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    const call = vi
      .mocked(toast.error)
      .mock.calls.find((c) => c[0] === 'Failed to save backup settings')
    expect(call).toBeDefined()
    expect(call?.[1]).toBeUndefined()
  })
})
