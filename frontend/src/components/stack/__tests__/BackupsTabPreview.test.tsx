import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupsTab } from '../BackupsTab'

/**
 * The snapshot PREVIEW half of agent-os-nhiv. Kept in its own file rather than
 * added to BackupsTab.test.tsx because every case here needs the snapshot list
 * to load SUCCESSFULLY and the preview query to fail — the opposite fixture to
 * the repo-fault cases already in that file, which fail the list itself.
 *
 * That ordering is not a testing convenience, it is the only route to the bug.
 * With the repository unreachable from the start, listSnapshots 503s, BackupsTab
 * renders the repo-fault panel INSTEAD of the snapshot table, and the Preview
 * button lives inside a snapshot row that is therefore never rendered. The live
 * scenario is a mount dropping or a remote going away while the tab is already
 * open, which is what these fixtures reproduce.
 *
 * CAVEAT, stated rather than overclaimed: these tests mock @/lib/api WHOLESALE,
 * so they bypass the axios response interceptor that actually builds
 * `{ ...error.response.data, status }`. A green here proves the component
 * HANDLES that shape; it does not prove the wire PRODUCES it.
 */

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

const mockStreamState = {
  status: 'idle' as const,
  lines: [] as string[],
  error: null as string | null,
  connect: vi.fn(),
  reset: vi.fn(),
}

vi.mock('@/hooks/useBackup', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/hooks/useBackup')>()
  return {
    ...actual,
    useBackupStreaming: () => mockStreamState,
  }
})

// ─── Fixtures ─────────────────────────────────────────────────────────────────

const STACK_ID = 'stacks~myapp'

function makeSnapshot() {
  return {
    id: 'abcdef1234567890abcdef1234567890abcdef12',
    shortId: 'abc12345',
    time: new Date(Date.now() - 3600000).toISOString(),
    hostname: 'mock-host',
    tags: ['stack:myapp'],
    paths: ['/stacks/myapp'],
    sizeBytes: 1048576,
  }
}

function makeRun() {
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
  }
}

function makePolicies() {
  return {
    policies: [
      {
        id: 'policy-1',
        targetType: 'stack' as const,
        targetId: STACK_ID,
        enabled: true,
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

/** Renders the tab, waits for the snapshot row, then opens its preview. */
async function openPreview() {
  const wrapper = createWrapper()
  render(<BackupsTab stackId={STACK_ID} />, { wrapper })
  const previewBtn = await screen.findByRole('button', { name: /show preview/i })
  fireEvent.click(previewBtn)
}

beforeEach(() => {
  vi.clearAllMocks()
  // The list loads. Every case here is "the repository died AFTER this".
  mockListSnapshots.mockResolvedValue([makeSnapshot()])
  mockGetHistory.mockResolvedValue({ runs: [makeRun()] })
  mockGetPolicies.mockResolvedValue(makePolicies())
  mockRestore.mockResolvedValue({ runId: 'run-1', wsUrl: '/ws/backups/restore/run-1' })
})

// ─── Tests ───────────────────────────────────────────────────────────────────

describe('BackupsTab — preview panel names the repository fault', () => {
  it('names an unreachable repository instead of "Failed to load preview."', async () => {
    mockPreviewSnapshot.mockRejectedValue({
      code: 'BACKUP_REPO_UNREACHABLE',
      message: 'backup repository did not answer',
      details: { repoState: 'unreachable' },
      status: 503,
    })

    await openPreview()

    await waitFor(() => {
      expect(screen.getByText('The backup repository could not be reached.')).toBeInTheDocument()
    })
    expect(screen.getByText(/check the remote or the mount/i)).toBeInTheDocument()
    expect(screen.getByText('backup repository did not answer')).toBeInTheDocument()
    expect(screen.queryByText('Failed to load preview.')).not.toBeInTheDocument()
  })

  it('names a rejected password rather than offering to initialise over the repository', async () => {
    mockPreviewSnapshot.mockRejectedValue({
      code: 'BACKUP_REPO_UNREACHABLE',
      message: 'restic rejected the configured password',
      details: { repoState: 'wrong_password' },
      status: 503,
    })

    await openPreview()

    await waitFor(() => {
      expect(screen.getByText('The restic password was rejected.')).toBeInTheDocument()
    })
    expect(screen.queryByText('Failed to load preview.')).not.toBeInTheDocument()
  })

  it('names a repository that does not exist yet', async () => {
    // previewSnapshot mints this code as well as the 503, which is why the
    // helper is reused whole rather than narrowed to one code for this site.
    mockPreviewSnapshot.mockRejectedValue({
      code: 'BACKUP_REPO_UNINITIALIZED',
      message: 'backup repository has not been initialised yet',
      details: { repoState: 'uninitialized' },
      status: 409,
    })

    await openPreview()

    await waitFor(() => {
      expect(screen.getByText('The backup repository does not exist yet.')).toBeInTheDocument()
    })
    expect(screen.queryByText('Failed to load preview.')).not.toBeInTheDocument()
  })

  it('keeps the generic sentence for a failure the helper does not recognise', async () => {
    // Criterion 3's negative arm. The discriminator is repoFaultFrom's own null
    // return, NOT any single code: previewSnapshotViaRestic's 500 is the
    // cleanest failure the helper declines to explain, and a well-formed id
    // naming no snapshot lands here too. Making EVERY failure a repository
    // story would be worse than the bug.
    mockPreviewSnapshot.mockRejectedValue({
      code: 'INTERNAL_ERROR',
      message: 'Failed to preview snapshot',
      status: 500,
    })

    await openPreview()

    await waitFor(() => {
      expect(screen.getByText('Failed to load preview.')).toBeInTheDocument()
    })
    // queryAllByText, not queryByText: the single-match form THROWS on more
    // than one hit, so a regression that rendered the fault twice would fail
    // this arm with a "found multiple elements" error rather than the
    // assertion, and the message would point at the wrong defect.
    expect(screen.queryAllByText(/backup repository/i)).toHaveLength(0)
  })

  it('keeps the generic sentence for a rejected snapshot id', async () => {
    mockPreviewSnapshot.mockRejectedValue({
      code: 'VALIDATION_ERROR',
      message: 'Invalid snapshot ID',
      status: 400,
    })

    await openPreview()

    await waitFor(() => {
      expect(screen.getByText('Failed to load preview.')).toBeInTheDocument()
    })
  })
})
