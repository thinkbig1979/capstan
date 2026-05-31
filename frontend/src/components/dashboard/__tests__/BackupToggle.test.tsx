import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BackupToggle } from '../BackupToggle'

// Radix UI Select uses scrollIntoView internally; jsdom does not implement it.
window.HTMLElement.prototype.scrollIntoView = vi.fn()

// ─── Hook mocks ───────────────────────────────────────────────────────────────
// Mirror the AutoUpdateToggle pattern: mock hooks directly rather than the API
// layer so the tests don't depend on react-query internals or async mutation flows.

const mockMutate = vi.fn()
let mockIsPending = false

vi.mock('@/hooks/useBackup', () => ({
  useToggleBackup: () => ({
    mutate: mockMutate,
    isPending: mockIsPending,
  }),
  useBackupPolicies: vi.fn(),
  useBackupStatus: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { useBackupPolicies, useBackupStatus } from '@/hooks/useBackup'

// ─── Fixture factories ────────────────────────────────────────────────────────

function makePolicy(enabled: boolean, stopPolicy: 'stop' | 'hot' = 'stop') {
  return {
    data: {
      policies: [
        {
          id: 'policy-1',
          targetType: 'stack' as const,
          targetId: 'stacks~myapp',
          enabled,
          stopPolicy,
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
        },
      ],
    },
  }
}

function makeStatus(resticAvailable = true, repositoryInitialized = true) {
  return {
    data: {
      resticAvailable,
      rcloneAvailable: true,
      repositoryInitialized,
      enabledStackCount: 1,
      lastRun: null,
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    },
  }
}

function makeStatusWithLastRun(status: 'success' | 'failed') {
  return {
    data: {
      resticAvailable: true,
      rcloneAvailable: true,
      repositoryInitialized: true,
      enabledStackCount: 1,
      lastRun: {
        id: 'run-1',
        kind: 'backup' as const,
        trigger: 'manual' as const,
        status,
        startedAt: new Date().toISOString(),
        finishedAt: new Date().toISOString(),
        stacksTotal: 1,
        stacksOk: status === 'success' ? 1 : 0,
        stacksFailed: status === 'failed' ? 1 : 0,
        bytesAdded: 1024,
        errorMessage: '',
      },
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    },
  }
}

const STACK_ID = 'stacks~myapp'

beforeEach(() => {
  vi.clearAllMocks()
  mockIsPending = false
  ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(false))
  ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus())
})

// ─── Tests: disabled state (engine unavailable) ───────────────────────────────

describe('BackupToggle — disabled state (engine unavailable)', () => {
  it('renders a disabled switch when restic is not available', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(false, false))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('renders a disabled switch when repository is not initialized', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(true, false))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('uses the data-testid for the disabled wrapper', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(false, false))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(document.querySelector(`[data-testid="backup-toggle-disabled-${STACK_ID}"]`)).toBeInTheDocument()
  })
})

// ─── Tests: enabled state ─────────────────────────────────────────────────────

describe('BackupToggle — enabled state', () => {
  it('renders the switch unchecked when policy has enabled=false', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(false))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` })).not.toBeChecked()
  })

  it('renders the switch checked when policy has enabled=true', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` })).toBeChecked()
  })

  it('renders the switch unchecked when no matching policy exists', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue({ data: { policies: [] } })
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` })).not.toBeChecked()
  })

  it('renders with the backup-toggle data-testid wrapper', () => {
    render(<BackupToggle stackId={STACK_ID} />)

    expect(document.querySelector(`[data-testid="backup-toggle-${STACK_ID}"]`)).toBeInTheDocument()
  })
})

// ─── Tests: toggle interaction ────────────────────────────────────────────────

describe('BackupToggle — toggle interaction', () => {
  it('calls mutate with enabled=true when toggling on from disabled state', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(false))
    render(<BackupToggle stackId={STACK_ID} />)

    fireEvent.click(screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` }))

    expect(mockMutate).toHaveBeenCalledWith(
      expect.objectContaining({ stackId: STACK_ID, enabled: true }),
      expect.objectContaining({ onError: expect.any(Function) }),
    )
  })

  it('calls mutate with enabled=false when toggling off from enabled state', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    render(<BackupToggle stackId={STACK_ID} />)

    fireEvent.click(screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` }))

    expect(mockMutate).toHaveBeenCalledWith(
      expect.objectContaining({ stackId: STACK_ID, enabled: false }),
      expect.objectContaining({ onError: expect.any(Function) }),
    )
  })

  it('reflects optimistic enabled=true before server responds (switch is checked immediately)', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(false))
    render(<BackupToggle stackId={STACK_ID} />)

    const switchEl = screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` })
    fireEvent.click(switchEl)

    // Optimistic update — switch should appear checked immediately.
    expect(switchEl).toBeChecked()
  })

  it('does not call mutate when switch is disabled (engine unavailable)', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(false, false))
    render(<BackupToggle stackId={STACK_ID} />)

    fireEvent.click(screen.getByRole('switch'))

    expect(mockMutate).not.toHaveBeenCalled()
  })
})

// ─── Tests: stop policy select ────────────────────────────────────────────────

describe('BackupToggle — stop policy select', () => {
  it('renders the stop policy select when backup is enabled', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true, 'stop'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('combobox', { name: /stop policy/i })).toBeInTheDocument()
  })

  it('does not render stop policy select when backup is disabled', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(false))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.queryByRole('combobox', { name: /stop policy/i })).not.toBeInTheDocument()
  })

  it('calls mutate with stopPolicy when stop policy select changes', async () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true, 'stop'))
    render(<BackupToggle stackId={STACK_ID} />)

    // Open the select.
    const select = screen.getByRole('combobox', { name: /stop policy/i })
    fireEvent.click(select)

    // Select the "Back up live" (hot) option.
    const hotOption = await screen.findByRole('option', { name: /back up live/i })
    fireEvent.click(hotOption)

    expect(mockMutate).toHaveBeenCalledWith(
      expect.objectContaining({ stopPolicy: 'hot' }),
      expect.objectContaining({ onError: expect.any(Function) }),
    )
  })

  it('passes the current stopPolicy value from the policy to the select', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true, 'hot'))
    render(<BackupToggle stackId={STACK_ID} />)

    // The select trigger displays the current value label.
    expect(screen.getByText(/back up live/i)).toBeInTheDocument()
  })
})

// ─── Tests: last run status indicator ─────────────────────────────────────────

describe('BackupToggle — last run status indicator', () => {
  it('shows success icon when last run status is success', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('success'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByLabelText('Last backup succeeded')).toBeInTheDocument()
  })

  it('shows failure icon when last run status is failed', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('failed'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByLabelText('Last backup failed')).toBeInTheDocument()
  })

  it('shows no status icon when lastRun is null', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus())
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.queryByLabelText('Last backup succeeded')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup failed')).not.toBeInTheDocument()
  })
})
