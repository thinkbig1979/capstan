import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { BackupStatusCard } from '../BackupStatusCard'
import type { BackupRun, BackupStatus } from '@/types'

// ─── Hook mocks ───────────────────────────────────────────────────────────────
// Mirror the BackupToggle.test.tsx pattern: mock the hooks directly rather than
// the API layer so tests don't depend on react-query internals or WS streaming.

const mockRunBackupMutate = vi.fn()

vi.mock('@/hooks/useBackup', () => ({
  useBackupStatus: vi.fn(),
  useRunBackup: () => ({
    mutate: mockRunBackupMutate,
    isPending: false,
  }),
  useBackupStreaming: () => ({
    status: 'idle',
    lines: [],
    error: null,
    connect: vi.fn(),
    reset: vi.fn(),
  }),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

import { useBackupStatus } from '@/hooks/useBackup'

// ─── Fixture factory ──────────────────────────────────────────────────────────
// Typed against BackupRun so a fixture that forgets `stacksTotal` fails to
// compile rather than silently defaulting to `undefined === 0` (false).

function makeRun(overrides: Partial<BackupRun>): BackupRun {
  return {
    id: 'run-1',
    kind: 'backup',
    trigger: 'manual',
    status: 'success',
    startedAt: new Date().toISOString(),
    finishedAt: new Date().toISOString(),
    stacksTotal: 1,
    stacksOk: 1,
    stacksFailed: 0,
    bytesAdded: 1024,
    errorMessage: '',
    ...overrides,
  }
}

function makeStatus(lastRun: BackupRun | null): BackupStatus {
  return {
    resticAvailable: true,
    rcloneAvailable: true,
    repositoryInitialized: true,
    enabledStackCount: 1,
    lastRun,
    nextRunAt: null,
    repoSizeBytes: null,
    schedulerRunning: false,
  }
}

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

function renderCard() {
  return render(<BackupStatusCard />, { wrapper })
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('BackupStatusCard — zero-stack backup badge', () => {
  it('does NOT show a green Success badge for a backup run that backed up zero stacks', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
      data: makeStatus(makeRun({ kind: 'backup', status: 'success', stacksTotal: 0, stacksOk: 0 })),
      isLoading: false,
    })
    renderCard()

    expect(screen.queryByText('Success')).not.toBeInTheDocument()
    expect(screen.getByText('No stacks backed up')).toBeInTheDocument()
  })

  it('still shows the green Success badge for a backup run that backed up stacks', () => {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
      data: makeStatus(makeRun({ kind: 'backup', status: 'success', stacksTotal: 2, stacksOk: 2 })),
      isLoading: false,
    })
    renderCard()

    expect(screen.getByText('Success')).toBeInTheDocument()
    expect(screen.queryByText('No stacks backed up')).not.toBeInTheDocument()
  })

  it.each(['restore', 'sync', 'dr_restore', 'prune'] as const)(
    'still shows the green Success badge for a zero-stack %s run (kind gate)',
    (kind) => {
      ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
        data: makeStatus(makeRun({ kind, status: 'success', stacksTotal: 0, stacksOk: 0 })),
        isLoading: false,
      })
      renderCard()

      expect(screen.getByText('Success')).toBeInTheDocument()
      expect(screen.queryByText('No stacks backed up')).not.toBeInTheDocument()
    },
  )
})
