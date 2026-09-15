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

function makeStatus(
  lastRun: BackupRun | null,
  overrides: Partial<BackupStatus> = {},
): BackupStatus {
  return {
    resticAvailable: true,
    rcloneAvailable: true,
    repoState: 'ok',
    repoStateMessage: '',
    enabledStackCount: 1,
    lastRun,
    nextRunAt: null,
    repoSizeBytes: null,
    schedulerRunning: false,
    ...overrides,
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

/**
 * agent-os-ssqt. The "Back up now" button gates on the same question the stack
 * toggle asks -- "can we back up RIGHT NOW" -- so its predicate is
 * repoState === 'ok', and every other state, fault or not-yet-probed, disables it.
 */
describe('BackupStatusCard — engine availability', () => {
  function renderWithState(overrides: Partial<BackupStatus>) {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
      data: makeStatus(null, overrides),
      isLoading: false,
    })
    renderCard()
    return screen.getByRole('button', { name: /back up now/i }) as HTMLButtonElement
  }

  it('ENABLES Back up now when the repository is reachable and initialised', () => {
    // The control arm (criterion 4). Every disabled-state assertion below is
    // satisfied by a card that disables the button unconditionally; this is the
    // one that is not.
    expect(renderWithState({ repoState: 'ok' }).disabled).toBe(false)
  })

  it.each([
    ['no repository exists', 'uninitialized' as const],
    ['the repository exists but is unreachable', 'unreachable' as const],
    ['the settings naming it could not be read', 'settings_unreadable' as const],
    ['nothing probed it', '' as const],
  ])('disables Back up now when %s', (_label, repoState) => {
    expect(renderWithState({ repoState }).disabled).toBe(true)
  })

  it('disables Back up now when restic is absent, whatever the repository state', () => {
    expect(renderWithState({ resticAvailable: false, repoState: 'ok' }).disabled).toBe(true)
  })
})

/**
 * The same in-class sibling as BackupToggle's tooltip: the banner's else-arm is
 * reached by every non-`ok` state, so a hardcoded "not initialised" was false
 * for three of them. It now renders the server's own sentence (agent-os-ssqt).
 */
describe('BackupStatusCard — the unavailable banner names the fault the server found', () => {
  function bannerText(overrides: Partial<BackupStatus>) {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
      data: makeStatus(null, overrides),
      isLoading: false,
    })
    renderCard()
    return screen.getByText(/Backup engine unavailable/i).parentElement?.textContent ?? ''
  }

  it('names the unreachable cause and does NOT claim the repository is uninitialised', () => {
    const text = bannerText({
      repoState: 'unreachable',
      repoStateMessage: 'repository not reachable: connection refused',
    })

    expect(text).toContain('repository not reachable: connection refused')
    expect(text).not.toMatch(/not initialised/i)
  })

  it('does NOT claim uninitialised when the settings themselves could not be read', () => {
    const text = bannerText({
      repoState: 'settings_unreadable',
      repoStateMessage: 'backup settings could not be read; repository state is unknown',
    })

    expect(text).toContain('backup settings could not be read')
    expect(text).not.toMatch(/not initialised/i)
  })

  it('still says so when the repository genuinely has never been initialised', () => {
    // The control: the sentence was true in this state and must survive.
    const text = bannerText({
      repoState: 'uninitialized',
      repoStateMessage: 'backup repository has not been initialised yet',
    })

    expect(text).toContain('backup repository has not been initialised yet')
  })

  it('leaves the restic-absent arm alone', () => {
    const text = bannerText({
      resticAvailable: false,
      repoState: '',
      repoStateMessage: 'restic binary not found in PATH',
    })

    expect(text).toContain('restic is not installed.')
  })
})
