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
      repositoryInitialized: true,
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
  status: 'idle' as 'idle' | 'running' | 'success' | 'error',
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
  status: 'success' | 'failed' | 'partial' | 'running'
  kind: 'backup' | 'sync' | 'restore' | 'dr_restore' | 'prune'
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
    const wrapper = createWrapper()
    render(<BackupsTab stackId={STACK_ID} />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText('No snapshots yet')).toBeInTheDocument()
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

    // useBackupSnapshots has retry:1 so allow up to 3s for the retry + error render.
    await waitFor(
      () => {
        expect(screen.getByText(/failed to load snapshots/i)).toBeInTheDocument()
      },
      { timeout: 3000 },
    )
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
})
