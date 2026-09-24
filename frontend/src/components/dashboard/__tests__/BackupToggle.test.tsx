import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BackupToggle } from '../BackupToggle'
import { BackupPoliciesRefreshNotice } from '../BackupPoliciesRefreshNotice'
import type { BackupRun, BackupStatus } from '@/types'

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

function makeStatus(
  resticAvailable = true,
  repoState: BackupStatus['repoState'] = 'ok',
  repoStateMessage = '',
) {
  return {
    data: {
      resticAvailable,
      rcloneAvailable: true,
      repoState,
      repoStateMessage,
      enabledStackCount: 1,
      lastRun: null,
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    },
  }
}

function makeStatusWithLastRun(
  status: 'success' | 'failed' | 'interrupted' | 'partial' | 'running',
  over: Partial<BackupRun> = {},
) {
  return {
    data: {
      resticAvailable: true,
      rcloneAvailable: true,
      repoState: 'ok',
      repoStateMessage: '',
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
        errorMessage: status === 'interrupted' ? 'process stopped before this run completed' : '',
        ...over,
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
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(false, ''))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('renders a disabled switch when repository is not initialized', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(true, 'uninitialized'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('renders a disabled switch when the repository EXISTS but is unreachable', () => {
    // agent-os-ssqt. The predicate is repoState === 'ok', not "not
    // uninitialized": a repository that went unreachable cannot be backed up
    // either, and the old boolean happened to cover this only by accident.
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(
      makeStatus(true, 'unreachable'),
    )
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('renders an ENABLED switch when the repository is reachable and initialised', () => {
    // The control arm (agent-os-ssqt criterion 4). Without it every assertion
    // above is satisfied by a component that disables the switch unconditionally.
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(true, 'ok'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByRole('switch')).not.toBeDisabled()
    expect(
      document.querySelector(`[data-testid="backup-toggle-disabled-${STACK_ID}"]`),
    ).not.toBeInTheDocument()
  })

  it('uses the data-testid for the disabled wrapper', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(false, ''))
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
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus(false, ''))
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

  it('shows an interrupted icon (not a failure icon) when last run status is interrupted', () => {
    // agent-os-pid: a swept run never reported a real outcome and may have
    // succeeded on the original instance, so it must not render as "failed".
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('interrupted'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByLabelText('Last backup was interrupted')).toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup failed')).not.toBeInTheDocument()
  })

  // agent-os-4zx0: a partial run (some stacks failed) rendered no icon at all,
  // the same as "never ran".
  it('shows a partial icon when last run status is partial', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('partial'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByLabelText('Last backup partly failed')).toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup succeeded')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup failed')).not.toBeInTheDocument()
  })

  it('still shows no icon while the last run is running', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('running'))
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.queryByLabelText(/^Last /)).not.toBeInTheDocument()
  })

  // agent-os-4zx0: lastRun is the newest run of ANY kind, so a restore or a
  // prune used to read "Last backup ...".
  it.each([
    ['restore', 'failed', 'Last restore failed'],
    ['prune', 'success', 'Last prune succeeded'],
    ['sync', 'partial', 'Last sync partly failed'],
    ['dr_restore', 'interrupted', 'Last DR restore was interrupted'],
    ['verify', 'success', 'Last repository check succeeded'],
  ] as const)('names the kind of a %s run (%s)', (kind, status, label) => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(
      makeStatusWithLastRun(status, { kind, stacksTotal: 0, stacksOk: 0, stacksFailed: 0 }),
    )
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByLabelText(label)).toBeInTheDocument()
    expect(screen.queryByLabelText(/^Last backup/)).not.toBeInTheDocument()
  })

  // Same case BackupStatusCard's LastRunBadge renders as "No stacks backed up"
  // (agent-os-a9gi): a successful backup that attempted no stacks.
  it('does not call a backup of zero stacks a success', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(
      makeStatusWithLastRun('success', { stacksTotal: 0, stacksOk: 0 }),
    )
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.getByLabelText('Last backup ran with no stacks')).toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup succeeded')).not.toBeInTheDocument()
  })

  it('shows no status icon when lastRun is null', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatus())
    render(<BackupToggle stackId={STACK_ID} />)

    expect(screen.queryByLabelText('Last backup succeeded')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup failed')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Last backup was interrupted')).not.toBeInTheDocument()
  })
})

// ─── Tests: showLastRunStatus suppression ─────────────────────────────────────

describe('BackupToggle — showLastRunStatus=false suppresses the icon', () => {
  // agent-os-lak4.5: statusData.lastRun is install-wide, not per-stack, so a
  // multi-row table would render the same icon on every row and assert an
  // outcome for stacks that were never backed up. The table opts out.

  it('renders no success icon when showLastRunStatus is false', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('success'))
    render(<BackupToggle stackId={STACK_ID} showLastRunStatus={false} />)

    expect(screen.queryByLabelText('Last backup succeeded')).not.toBeInTheDocument()
  })

  it('renders no failure icon when showLastRunStatus is false', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('failed'))
    render(<BackupToggle stackId={STACK_ID} showLastRunStatus={false} />)

    expect(screen.queryByLabelText('Last backup failed')).not.toBeInTheDocument()
  })

  it('renders no interrupted icon when showLastRunStatus is false', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(
      makeStatusWithLastRun('interrupted'),
    )
    render(<BackupToggle stackId={STACK_ID} showLastRunStatus={false} />)

    expect(screen.queryByLabelText('Last backup was interrupted')).not.toBeInTheDocument()
  })

  it('still renders the switch and the stop-policy select when the icon is suppressed', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true, 'stop'))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('success'))
    render(<BackupToggle stackId={STACK_ID} showLastRunStatus={false} />)

    expect(screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` })).toBeChecked()
    expect(screen.getByRole('combobox', { name: /stop policy/i })).toBeInTheDocument()
  })

  it('renders the success icon when showLastRunStatus is explicitly true', () => {
    // The must-pass side of the guard: the prop suppresses only when false.
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue(makePolicy(true))
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(makeStatusWithLastRun('success'))
    render(<BackupToggle stackId={STACK_ID} showLastRunStatus />)

    expect(screen.getByLabelText('Last backup succeeded')).toBeInTheDocument()
  })
})

/**
 * agent-os-ssqt, the in-class sibling. The locked tooltip's else-arm is reached
 * by `unreachable`, `settings_unreadable` and `''` as well as `uninitialized`,
 * so a hardcoded "Backup repository not initialised." asserted the same
 * falsehood the deleted boolean did -- in prose, where no sweep keyed on the
 * field name could ever find it. It now renders the server's own sentence.
 */
describe('BackupToggle — the locked tooltip names the fault the server found', () => {
  function lockedTooltipText(status: ReturnType<typeof makeStatus>) {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue(status)
    render(<BackupToggle stackId={STACK_ID} />)
    const trigger = document.querySelector(
      `[data-testid="backup-toggle-disabled-${STACK_ID}"]`,
    ) as HTMLElement
    fireEvent.focus(trigger)
    return screen.getByRole('tooltip').textContent ?? ''
  }

  it('names the unreachable cause and does NOT claim the repository is uninitialised', () => {
    // The load-bearing arm. A repository that went unreachable still holds
    // every backup the user has; telling them it was never initialised invites
    // the one recovery that replaces it with an empty one.
    const text = lockedTooltipText(
      makeStatus(true, 'unreachable', 'repository not reachable: connection refused'),
    )

    expect(text).toContain('repository not reachable: connection refused')
    expect(text).not.toMatch(/not initialised/i)
  })

  it('does NOT claim uninitialised when the settings themselves could not be read', () => {
    const text = lockedTooltipText(
      makeStatus(
        true,
        'settings_unreadable',
        'backup settings could not be read; repository state is unknown',
      ),
    )

    expect(text).toContain('backup settings could not be read')
    expect(text).not.toMatch(/not initialised/i)
  })

  it('still says so when the repository genuinely has never been initialised', () => {
    // The control. The claim was never wrong in THIS state, and the fix must
    // not have been bought by removing a true sentence along with the false one.
    const text = lockedTooltipText(
      makeStatus(true, 'uninitialized', 'backup repository has not been initialised yet'),
    )

    expect(text).toContain('backup repository has not been initialised yet')
  })

  it('leaves the restic-absent arm alone', () => {
    const text = lockedTooltipText(makeStatus(false, '', 'restic binary not found in PATH'))

    expect(text).toContain('restic / rclone not installed.')
  })
})

// ─── Tests: no write seeded from policies the server never sent ───────────────

describe('BackupToggle — no write is seeded without loaded policies (agent-os-r6fx)', () => {
  it('first load failed: no switch to click, an error with Retry instead', () => {
    const refetch = vi.fn()
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue({
      data: undefined,
      isError: true,
      refetch,
    })
    render(<BackupToggle stackId={STACK_ID} />)

    // Pre-fix: an ENABLED switch seeded enabled=false / stopPolicy='stop', and
    // one click wrote both for a stack whose real policy nobody had read.
    const switchEl = screen.queryByRole('switch')
    if (switchEl) fireEvent.click(switchEl)
    expect(mockMutate).not.toHaveBeenCalled()
    expect(switchEl).toBeNull()

    fireEvent.click(
      screen.getByRole('button', { name: 'Could not load the backup policy. Retry' }),
    )
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('still loading: nothing to click either', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
    })
    render(<BackupToggle stackId={STACK_ID} />)

    const switchEl = screen.queryByRole('switch')
    if (switchEl) fireEvent.click(switchEl)
    expect(mockMutate).not.toHaveBeenCalled()
    expect(switchEl).toBeNull()
  })

  it('loaded with no policy for this stack: a real "off", and it can be switched on', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue({ data: { policies: [] } })
    render(<BackupToggle stackId={STACK_ID} />)

    const switchEl = screen.getByRole('switch', { name: `Backup stack ${STACK_ID}` })
    expect(switchEl).not.toBeChecked()
    fireEvent.click(switchEl)
    expect(mockMutate).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('button', { name: /Could not load/ })).toBeNull()
  })
})

describe('BackupPoliciesRefreshNotice — a failed refetch is disclosed (agent-os-r6fx)', () => {
  const notice = /Could not refresh the backup settings\. The values shown are the last ones the server sent, so check them before saving\./

  it('policies loaded, then a refetch failed: the notice, with Retry', () => {
    const refetch = vi.fn()
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue({
      ...makePolicy(true, 'hot'),
      isError: true,
      refetch,
    })
    render(<BackupPoliciesRefreshNotice />)

    expect(screen.getByText(notice)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('fresh policies: no notice', () => {
    render(<BackupPoliciesRefreshNotice />)
    expect(screen.queryByText(notice)).toBeNull()
  })

  it('first load failed: no "last ones the server sent" claim', () => {
    ;(useBackupPolicies as ReturnType<typeof vi.fn>).mockReturnValue({ data: undefined, isError: true })
    render(<BackupPoliciesRefreshNotice />)
    expect(screen.queryByText(notice)).toBeNull()
  })
})
