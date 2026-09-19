import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupsTab } from '../BackupsTab'

// ─── API mocks ───────────────────────────────────────────────────────────────

const mockListSnapshots = vi.fn()
const mockPreviewSnapshot = vi.fn()
const mockRestore = vi.fn()
const mockGetHistory = vi.fn()
const mockGetPolicies = vi.fn()

vi.mock('@/lib/api', () => ({
  backupApi: {
    listSnapshots: (...args: unknown[]) => mockListSnapshots(...args),
    previewSnapshot: (...args: unknown[]) => mockPreviewSnapshot(...args),
    restore: (...args: unknown[]) => mockRestore(...args),
    getHistory: (...args: unknown[]) => mockGetHistory(...args),
    getPolicies: (...args: unknown[]) => mockGetPolicies(...args),
    getStatus: vi.fn().mockResolvedValue({
      resticAvailable: true,
      rcloneAvailable: true,
      repoState: 'ok',
      repoStateMessage: '',
      enabledStackCount: 1,
      lastRun: null,
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    }),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// Mock the WebSocket streaming hook so tests never open a real socket.
// Default state is idle (no streaming in progress).
const mockStreamConnect = vi.fn()
const mockStreamReset = vi.fn()
const mockStreamState = {
  status: 'idle' as 'idle' | 'running' | 'success' | 'partial' | 'error' | 'unavailable',
  lines: [] as string[],
  error: null as string | null,
  connect: mockStreamConnect,
  reset: mockStreamReset,
}

vi.mock('@/hooks/useBackup', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/hooks/useBackup')>()
  return {
    ...actual,
    useBackupStreaming: () => mockStreamState,
  }
})

// ─── Fixtures ─────────────────────────────────────────────────────────────────

function makeSnapshot(overrides: Partial<{
  id: string
  shortId: string
  time: string
  tags: string[]
  sizeBytes: number
}> = {}) {
  return {
    id: 'abcdef1234567890abcdef1234567890abcdef12',
    shortId: 'abc12345',
    time: new Date(Date.now() - 3600000).toISOString(),
    hostname: 'mock-host',
    tags: ['stack:myapp'],
    paths: ['/stacks/myapp'],
    sizeBytes: 1048576,
    ...overrides,
  }
}

function makeRun(overrides: Partial<{
  id: string
  status: 'success' | 'failed' | 'partial' | 'running' | 'interrupted'
  kind: 'backup' | 'sync' | 'restore' | 'dr_restore' | 'prune' | 'verify'
}> = {}) {
  return {
    id: 'run-1',
    kind: 'backup' as const,
    trigger: 'manual' as const,
    status: 'success' as const,
    startedAt: new Date(Date.now() - 3600000).toISOString(),
    finishedAt: new Date(Date.now() - 3540000).toISOString(),
    stacksTotal: 1,
    stacksOk: 1,
    stacksFailed: 0,
    bytesAdded: 1048576,
    errorMessage: '',
    ...overrides,
  }
}

function makePolicies(enabled = true) {
  return {
    policies: [
      {
        id: 'policy-1',
        targetType: 'stack' as const,
        targetId: 'stacks~myapp',
        enabled,
        stopPolicy: 'stop' as const,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
      },
    ],
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

const STACK_ID = 'stacks~myapp'

beforeEach(() => {
  vi.clearAllMocks()
  mockStreamState.status = 'idle'
  mockStreamState.lines = []
  mockStreamState.error = null
  mockListSnapshots.mockResolvedValue([makeSnapshot()])
  mockGetHistory.mockResolvedValue({ runs: [makeRun()] })
  mockGetPolicies.mockResolvedValue(makePolicies(true))
  mockPreviewSnapshot.mockResolvedValue({
    entries: ['/stacks/myapp/compose.yaml', '/stacks/myapp/.env'],
  })
  mockRestore.mockResolvedValue({
    runId: 'run-mock-3',
    wsUrl: '/ws/backups/restore/run-mock-3',
  })
})

// ─── Tests ───────────────────────────────────────────────────────────────────

describe('BackupsTab — empty state', () => {
  it('renders EmptyState when there are no snapshots', async () => {
    mockListSnapshots.mockResolvedValue([])
    // No successful backup runs, so this is a genuinely-empty repo, not the
    // ST-3 "ran but nothing listed" reconciliation branch.
    mockGetHistory.mockResolvedValue({ runs: [] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('No snapshots yet')).toBeInTheDocument()
    })
  })

  it('reconciles the empty state when runs succeeded but no snapshots are listed (ST-3)', async () => {
    mockListSnapshots.mockResolvedValue([])
    mockGetHistory.mockResolvedValue({ runs: [makeRun({ status: 'success' })] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('No snapshots listed')).toBeInTheDocument()
    })
  })

  it('renders EmptyState for runs when there are no run records', async () => {
    mockGetHistory.mockResolvedValue({ runs: [] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('No backup runs yet')).toBeInTheDocument()
    })
  })

  it('shows a "backup not enabled" notice when backup policy is disabled', async () => {
    mockGetPolicies.mockResolvedValue(makePolicies(false))
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/backup is not enabled for this stack/i)).toBeInTheDocument()
    })
  })
})

describe('BackupsTab — snapshots table', () => {
  it('renders snapshots table with shortId, tags, and size', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('abc12345')).toBeInTheDocument()
      expect(screen.getByText('stack:myapp')).toBeInTheDocument()
      // 1.0 MB appears in both the snapshot size column and the run bytes-added column.
      expect(screen.getAllByText('1.0 MB').length).toBeGreaterThanOrEqual(1)
    })
  })

  it('renders "—" when sizeBytes is null/undefined', async () => {
    mockListSnapshots.mockResolvedValue([makeSnapshot({ sizeBytes: undefined })])
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      // "—" appears at least once for the size column.
      expect(screen.getAllByText('—').length).toBeGreaterThan(0)
    })
  })

  it('renders the "Failed to load snapshots" error when the query fails', async () => {
    mockListSnapshots.mockRejectedValue(new Error('Network error'))
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    // useBackupSnapshots no longer restates `retry: 1` over the wrapper's
    // `retry: false` (agent-os-tdts), so this settles on the first failure. The
    // timeout is headroom for a loaded box, not a retry backoff.
    await waitFor(
      () => {
        expect(screen.getByText(/failed to load snapshots/i)).toBeInTheDocument()
      },
      { timeout: 3000 },
    )
  })
})

describe('BackupsTab — repository fault (agent-os-eo4u)', () => {
  // The 503 body repoFault() mints in backend/internal/handlers/backup.go, shaped
  // as the axios interceptor in lib/api.ts rejects it: the response body spread
  // with the status injected. It is hand-built here because vi.mock('@/lib/api')
  // above replaces the api module, so the real interceptor never runs in this
  // file. That this shape IS what the interceptor produces is pinned separately,
  // against the real interceptor, in src/lib/__tests__/apiInterceptorError.test.ts.
  function repoFault(repoState: string, message: string) {
    return {
      code: 'BACKUP_REPO_UNREACHABLE',
      message,
      details: { repoState },
      status: 503,
    }
  }

  it('names an unreachable repository as the cause, not a generic failure', async () => {
    mockListSnapshots.mockRejectedValue(
      repoFault('unreachable', 'repository not reachable: dial tcp: connection refused'),
    )
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/your snapshots are not missing/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/failed to load snapshots/i)).not.toBeInTheDocument()
  })

  it('names unreadable backup settings as the cause', async () => {
    mockListSnapshots.mockRejectedValue(
      repoFault(
        'settings_unreadable',
        'backup settings could not be read; repository state is unknown',
      ),
    )
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/could not load the backup settings/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/failed to load snapshots/i)).not.toBeInTheDocument()
  })

  it('surfaces the cause sentence the backend computed', async () => {
    mockListSnapshots.mockRejectedValue(
      repoFault('unreachable', 'repository not reachable: dial tcp: connection refused'),
    )
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(
        screen.getByText(/repository not reachable: dial tcp: connection refused/i),
      ).toBeInTheDocument()
    })
  })

  it('prescribes no recovery for a repoState it does not recognise', async () => {
    // The two known states are positive arms; anything else falls back. A future
    // backend repoState must NOT inherit advice written for 'unreachable' —
    // "initialising would not bring them back" could be exactly wrong for it.
    mockListSnapshots.mockRejectedValue(repoFault('some_future_state', 'a new fault'))
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/could not determine the cause/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/would not bring them back/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/failed to load snapshots/i)).not.toBeInTheDocument()
  })

  it('leaves every other query failure on the generic message', async () => {
    // Two-sided against the arms above on one instrument: a rejection that is NOT
    // the repository fault must keep the original sentence. Guards against a fix
    // that turns every snapshots failure into a repository story.
    mockListSnapshots.mockRejectedValue({ code: 'INTERNAL_ERROR', message: 'boom', status: 500 })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/failed to load snapshots/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/your snapshots are not missing/i)).not.toBeInTheDocument()
  })

  // ─── agent-os-l04z / 9f5c / rg8h ────────────────────────────────────────
  //
  // These arms exist because tsc CANNOT pin them. repoFaultFrom casts
  // details.repoState to a plain `string` and has a `default` arm, so the
  // compiler sees no missing case and stays green however many states the
  // backend mints. The RepositorySection `satisfies Record` failure covers the
  // settings display and nothing here; every arm below was seen failing first.

  it('points a missing restic password at Backup settings, never at the mount', async () => {
    // The DEFAULT state of a fresh install: both shipped compose files leave
    // RESTIC_PASSWORD commented out. It used to arrive as 'unreachable' and be
    // answered with "check the remote or the mount" — hardware that is fine.
    //
    // The copy points at Backup settings rather than at the env var because the
    // password can be set from the settings UI as well as from RESTIC_PASSWORD,
    // and settings is the route that works in both deployments.
    mockListSnapshots.mockRejectedValue(
      repoFault('password_missing', 'no restic password is configured, so the repository was not contacted'),
    )
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    // Matched on a phrase unique to THIS hint, not on /backup settings/i:
    // password_missing, wrong_password and uninitialized all point at Backup
    // settings, so the loose matcher survived a hint-swap mutant. "never
    // contacted" is true of this state alone — the other two both reached the
    // repository.
    await waitFor(() => {
      expect(screen.getByText(/never contacted/i)).toBeInTheDocument()
    })
    expect(screen.getByText(/set the restic password in backup settings/i)).toBeInTheDocument()
    expect(screen.queryByText(/check the remote or the mount/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/could not determine the cause/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/failed to load snapshots/i)).not.toBeInTheDocument()
    // The two sibling hints, excluded by name so a swap cannot pass.
    expect(screen.queryByText(/the credential is what is wrong/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/initialise the repository in backup settings, then run/i)).not.toBeInTheDocument()
  })

  it('names the credential, not the mount, when the password is rejected', async () => {
    // restic exit 12. The repository answered and was read far enough to try
    // the key; the remote and the mount are both working.
    mockListSnapshots.mockRejectedValue(
      repoFault('wrong_password', 'the configured restic password was rejected by the repository'),
    )
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/the credential is what is wrong/i)).toBeInTheDocument()
    })
    expect(screen.getByText(/your snapshots are not missing/i)).toBeInTheDocument()
    expect(screen.queryByText(/check the remote or the mount/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/could not determine the cause/i)).not.toBeInTheDocument()
    // The destructive-adjacent recovery must not be offered for a state where a
    // repository full of snapshots exists and only the key is wrong.
    expect(screen.queryByText(/initialise the repository in backup settings, then run/i)).not.toBeInTheDocument()
  })

  it('tells the operator to initialise when the repository does not exist yet', async () => {
    // A DIFFERENT CODE, not just a different repoState: repoFaultFrom returned
    // null for anything other than BACKUP_REPO_UNREACHABLE, so this needed a
    // new arm either way. Before agent-os-rg8h this state never reached the
    // frontend as a fault at all — it arrived as a 500 and rendered as the
    // generic "Failed to load snapshots."
    mockListSnapshots.mockRejectedValue({
      code: 'BACKUP_REPO_UNINITIALIZED',
      message: 'backup repository has not been initialised yet',
      details: { repoState: 'uninitialized' },
      status: 409,
    })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/initialise the repository/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/failed to load snapshots/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/run a backup to create the first one/i)).not.toBeInTheDocument()
  })

  it('names an absent restic binary instead of showing an empty repository', async () => {
    // agent-os-9f5c. This used to be 200 with an empty array, so the operator
    // saw the ordinary empty state and, with no successful runs in history,
    // "Run a backup to create the first one" — which cannot work, because the
    // binary that would run it is missing. frontend/src had ZERO arms for
    // BACKUP_UNAVAILABLE before this one.
    mockListSnapshots.mockRejectedValue({
      code: 'BACKUP_UNAVAILABLE',
      message: 'restic binary not found in PATH',
      details: { cause: 'restic_missing' },
      status: 409,
    })
    mockGetHistory.mockResolvedValue({ runs: [] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/restic binary not found in PATH/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/run a backup to create the first one/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/failed to load snapshots/i)).not.toBeInTheDocument()
  })

  it('still shows "Run a backup to create the first one" for a genuinely empty repository', async () => {
    // The OTHER SIDE of the two arms above, on the same instrument. Row 7 of the
    // state table: reachable, initialised, zero snapshots for this stack — which
    // is also the ordinary case for a new stack in a shared repository. The copy
    // is correct here and must survive being made unreachable everywhere else.
    // An implementation that removes it outright passes the arms above and is
    // worse than the bug.
    mockListSnapshots.mockResolvedValue([])
    mockGetHistory.mockResolvedValue({ runs: [] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('No snapshots yet')).toBeInTheDocument()
    })
    expect(screen.getByText(/run a backup to create the first one/i)).toBeInTheDocument()
  })

  it('drops the availability hedge now that a 200 proves the repository answered', async () => {
    // The inverse of what this arm asserted under agent-os-eo4u, and the
    // condition it named as its own expiry has now been met. It used to require
    // the hedge because listSnapshots answered 200 with an EMPTY ARRAY when
    // restic was absent, from an early return BEFORE CheckRepository — so this
    // state was reachable with availability genuinely unknown.
    //
    // agent-os-9f5c deleted that path. Reaching this branch now requires a 200,
    // which the handler emits only after CheckRepository returned RepoStateOK
    // and restic listed successfully. Telling an operator the repository "may be
    // unavailable" when it demonstrably answered is the wrong-fault-named defect
    // this wave exists to remove.
    mockListSnapshots.mockResolvedValue([])
    mockGetHistory.mockResolvedValue({ runs: [makeRun({ status: 'success' })] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('No snapshots listed')).toBeInTheDocument()
    })
    expect(screen.getByText(/the repository answered/i)).toBeInTheDocument()
    expect(screen.queryByText(/may be unavailable/i)).not.toBeInTheDocument()
  })
})

describe('BackupsTab — restore flow', () => {
  it('opens the ConfirmDialog when Restore is clicked', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const restoreBtn = await screen.findByRole('button', { name: /restore snapshot abc12345/i })
    fireEvent.click(restoreBtn)

    await waitFor(() => {
      expect(screen.getByText('Restore snapshot')).toBeInTheDocument()
      // Confirm dialog has Restore and Cancel buttons.
      expect(screen.getByRole('button', { name: 'Restore' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    })
  })

  it('does not call restore mutation when Cancel is clicked in ConfirmDialog', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const restoreBtn = await screen.findByRole('button', { name: /restore snapshot abc12345/i })
    fireEvent.click(restoreBtn)

    const cancelBtn = await screen.findByRole('button', { name: 'Cancel' })
    fireEvent.click(cancelBtn)

    expect(mockRestore).not.toHaveBeenCalled()
  })

  it('calls restore mutation with stackId and snapshotId when Restore is confirmed', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const restoreBtn = await screen.findByRole('button', { name: /restore snapshot abc12345/i })
    fireEvent.click(restoreBtn)

    const confirmBtn = await screen.findByRole('button', { name: 'Restore' })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(mockRestore).toHaveBeenCalledWith({
        stackId: STACK_ID,
        snapshotId: 'abcdef1234567890abcdef1234567890abcdef12',
        confirm: true,
      })
    })
  })

  it('calls stream.connect with the wsUrl path after successful restore mutation', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const restoreBtn = await screen.findByRole('button', { name: /restore snapshot abc12345/i })
    fireEvent.click(restoreBtn)

    const confirmBtn = await screen.findByRole('button', { name: 'Restore' })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(mockStreamConnect).toHaveBeenCalledWith(
        '/ws/backups/restore/run-mock-3',
        expect.any(Function),
      )
    })
  })

  it('strips /api/v1 prefix from wsUrl before passing to stream.connect', async () => {
    mockRestore.mockResolvedValue({
      runId: 'run-mock-3',
      wsUrl: '/api/v1/ws/backups/restore/run-mock-3',
    })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const restoreBtn = await screen.findByRole('button', { name: /restore snapshot abc12345/i })
    fireEvent.click(restoreBtn)

    const confirmBtn = await screen.findByRole('button', { name: 'Restore' })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(mockStreamConnect).toHaveBeenCalledWith(
        '/ws/backups/restore/run-mock-3',
        expect.any(Function),
      )
    })
  })
})

describe('BackupsTab — snapshot preview panel', () => {
  it('opens preview panel when Preview button is clicked', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const previewBtn = await screen.findByRole('button', { name: /show preview/i })
    fireEvent.click(previewBtn)

    await waitFor(() => {
      expect(screen.getByText('/stacks/myapp/compose.yaml')).toBeInTheDocument()
      expect(screen.getByText('/stacks/myapp/.env')).toBeInTheDocument()
    })
  })

  it('closes preview panel when close button is clicked', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const previewBtn = await screen.findByRole('button', { name: /show preview/i })
    fireEvent.click(previewBtn)

    await screen.findByText('/stacks/myapp/compose.yaml')

    const closeBtn = screen.getByRole('button', { name: 'Close preview' })
    fireEvent.click(closeBtn)

    expect(screen.queryByText('/stacks/myapp/compose.yaml')).not.toBeInTheDocument()
  })

  it('shows "No entries found" when preview has no entries', async () => {
    mockPreviewSnapshot.mockResolvedValue({ entries: [] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    const previewBtn = await screen.findByRole('button', { name: /show preview/i })
    fireEvent.click(previewBtn)

    await waitFor(() => {
      expect(screen.getByText(/no entries found in snapshot/i)).toBeInTheDocument()
    })
  })
})

describe('BackupsTab — recent runs table', () => {
  it('renders run kind, trigger, and status badge', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      // Kind column shows "backup" (capitalized by CSS, matched case-insensitively).
      expect(screen.getByText('backup')).toBeInTheDocument()
      // Trigger column.
      expect(screen.getByText('manual')).toBeInTheDocument()
      // RunStatusBadge for success.
      expect(screen.getByText('Success')).toBeInTheDocument()
    })
  })

  it('renders bytes added in the run row', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      // 1048576 bytes = 1.0 MB (same value appears in snapshots table too).
      expect(screen.getAllByText('1.0 MB').length).toBeGreaterThan(0)
    })
  })

  it('renders failed status badge for a failed run', async () => {
    mockGetHistory.mockResolvedValue({ runs: [makeRun({ status: 'failed' })] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Failed')).toBeInTheDocument()
    })
  })

  it('renders an interrupted status badge (not "Failed") for an interrupted run', async () => {
    // agent-os-pid: a swept run never reported a real outcome and may have
    // succeeded on the original instance, so it must get its own label.
    mockGetHistory.mockResolvedValue({ runs: [makeRun({ status: 'interrupted' })] })
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Interrupted')).toBeInTheDocument()
      expect(screen.queryByText('Failed')).not.toBeInTheDocument()
    })
  })
})

describe('BackupsTab — restore progress panel', () => {
  it('does not render progress panel when stream status is idle', async () => {
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await screen.findByText('abc12345')

    // The RestoreProgress component returns null when status is idle.
    expect(screen.queryByText('Restoring…')).not.toBeInTheDocument()
  })

  it('renders the restore running panel when stream status is running', async () => {
    mockStreamState.status = 'running'
    mockStreamState.lines = ['Starting restore…']
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Restoring…')).toBeInTheDocument()
      expect(screen.getByText('Starting restore…')).toBeInTheDocument()
    })
  })

  it('renders "Restore completed" header when stream status is success', async () => {
    mockStreamState.status = 'success'
    mockStreamState.lines = ['Backup completed successfully.']
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Restore completed')).toBeInTheDocument()
    })
  })

  it('renders "Restore failed" header when stream status is error', async () => {
    mockStreamState.status = 'error'
    mockStreamState.error = 'Something went wrong'
    mockStreamState.lines = ['Error: Something went wrong']
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Restore failed')).toBeInTheDocument()
    })
  })

  it('renders "Restore partially completed" header when stream status is partial', async () => {
    mockStreamState.status = 'partial'
    mockStreamState.lines = ['Backup partially completed.']
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('Restore partially completed')).toBeInTheDocument()
    })
  })

  // agent-os-mjrl: a viewer refused at the per-run attacher bound is a refused
  // STREAM, not a failed run. The panel must say so, name the limit and the
  // action, and must not show the failure header.
  it('renders a non-alarming "Live output unavailable" panel when stream status is unavailable', async () => {
    // The hook has already turned the backend reason into this sentence.
    const reason = 'This run already has 24 viewers. Close another viewer and reconnect.'
    mockStreamState.status = 'unavailable'
    mockStreamState.error = reason
    mockStreamState.lines = [`Stream unavailable: ${reason}`]
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('restore-progress-header')).toHaveTextContent('Live output unavailable')
    })
    expect(screen.getByTestId('restore-progress-unavailable')).toHaveTextContent(
      'This run already has 24 viewers. Close another viewer and reconnect. The restore continues on the server; check Recent runs for its result.',
    )
    expect(screen.queryByText('Restore failed')).not.toBeInTheDocument()
  })
})
