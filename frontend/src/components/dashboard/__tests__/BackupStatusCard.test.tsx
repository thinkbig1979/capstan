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
    lastVerify: null,
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

  // 'verify' belongs in this list for a real reason, not for symmetry: a verify
  // run inspects the repository rather than a set of stacks, so stacksTotal is
  // 0 by nature. The zero-stack guard must stay gated on kind === 'backup' or
  // every successful verification would render as "No stacks backed up".
  it.each(['restore', 'sync', 'dr_restore', 'prune', 'verify'] as const)(
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

// The two timestamps mirror the backend test named below: the verify fails
// FIRST, the backup succeeds AFTER it. T1 < T2 is the whole point.
const T1 = '2026-02-01T00:00:00Z'
const T2 = '2026-02-02T00:00:00Z'

const failedVerify = () =>
  makeRun({
    id: 'run-verify-failed',
    kind: 'verify',
    status: 'failed',
    startedAt: T1,
    errorMessage: 'repository integrity check failed',
  })

/**
 * agent-os-5lpz, mirroring TestGetStatus_LastVerifySurfacesFailure in
 * backend/internal/handlers/backup_verify_test.go. The backend reports
 * lastVerify separately from lastRun exactly so a later successful backup
 * cannot hide a failed verification, and lastVerify is the only channel by
 * which an operator learns the repository may not be restorable. The card must
 * therefore read lastVerify and never derive this from lastRun.
 */
describe('BackupStatusCard — a failed verify is not masked by a later successful backup', () => {
  function renderWith(overrides: Partial<BackupStatus>, lastRun: BackupRun | null = null) {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
      data: makeStatus(lastRun, overrides),
      isLoading: false,
    })
    renderCard()
  }

  it('surfaces a failed verify although a LATER backup succeeded', () => {
    renderWith(
      { lastVerify: failedVerify() },
      makeRun({ id: 'run-backup-ok', kind: 'backup', status: 'success', startedAt: T2 }),
    )

    const banner = screen.getByTestId('verify-failed-banner')
    expect(banner).toBeInTheDocument()
    expect(banner).toHaveTextContent('repository integrity check failed')
  })

  it('shows no banner when the last verify SUCCEEDED — control (i)', () => {
    renderWith({ lastVerify: makeRun({ kind: 'verify', status: 'success', startedAt: T1 }) })

    expect(screen.queryByTestId('verify-failed-banner')).not.toBeInTheDocument()
  })

  it('shows no banner when no verify has ever run — control (ii)', () => {
    renderWith({ lastVerify: null })

    expect(screen.queryByTestId('verify-failed-banner')).not.toBeInTheDocument()
  })

  it('still surfaces the failed verify when the engine reads as unavailable — control (iii)', () => {
    // The un-maskable property from the other direction. The j1jw defect was a
    // repository that answers from cache while its data is gone, so this
    // warning must be gated on neither the reachability probe nor lastRun.
    renderWith({
      repoState: 'unreachable',
      repoStateMessage: 'repository not reachable: connection refused',
      lastVerify: failedVerify(),
    })

    expect(screen.getByTestId('verify-failed-banner')).toBeInTheDocument()
  })
})

describe('BackupStatusCard — the "Last check" readout', () => {
  function renderWith(overrides: Partial<BackupStatus>) {
    ;(useBackupStatus as ReturnType<typeof vi.fn>).mockReturnValue({
      data: makeStatus(null, overrides),
      isLoading: false,
    })
    renderCard()
  }

  it('reports when the repository was last verified', () => {
    renderWith({
      lastVerify: makeRun({ kind: 'verify', status: 'success', startedAt: T1, stacksTotal: 0 }),
    })

    expect(screen.getByText('Last check')).toBeInTheDocument()
    expect(screen.getByText('Success')).toBeInTheDocument()
  })

  it('renders no cell at all when no verify has ever run — the operator has no trigger yet, so "Never" would be noise', () => {
    renderWith({ lastVerify: null })

    expect(screen.queryByText('Last check')).not.toBeInTheDocument()
  })
})
