import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'
import type { User } from '@/types'

// agent-os-mhqf. The boot probes retry a failure that carries no HTTP response,
// and a backend that ACCEPTS the connection but never answers produces exactly
// that. Nothing bounded a single attempt: the only ceiling was the shared axios
// client timeout (api.ts:62, 120000ms, sized for pull/start), so one hang cost
// 120s per attempt -- 3 attempts on checkStatus plus 3 on checkAuth, held
// sequentially behind one full-page spinner (App.tsx:105-106, :113), is ~720s.
//
// The fix is a per-attempt bound in authStore's two retry loops, so the numbers
// in the boot budget are related to each other instead of being independent
// accidents. The arithmetic this file pins: 3 attempts x 8s + 250ms + 750ms
// ~= 25s per probe, ~50s for the boot, against 60s of advanced time here.
//
// TWO-SIDED ON ONE INSTRUMENT, and the passing arm is the load-bearing half.
// Shortening a timeout until everything fails fast would satisfy the hang arm
// and break every genuinely slow-but-working backend, so the first arm answers
// slowly WITHIN the per-attempt budget and asserts the answer was USED: exactly
// one call to each probe, and the app on the branch that only that answer can
// reach. A fix that cut the slow probe off would render the login page instead
// and call each probe more than once.
//
// Assertions are on the RENDERED BRANCH, never on the absence of the spinner:
// 'Loading...' appears in several shipping components, and an absence assertion
// also passes when the render died or the ErrorBoundary caught.
vi.mock('@/components/layout/AppShell', () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="app-shell">{children}</div>
  ),
}))

vi.mock('@/pages/DashboardPage', () => ({
  DashboardPage: () => <div data-testid="dashboard" />,
}))

vi.mock('@/pages/SetupPage', () => ({
  SetupPage: () => <div data-testid="setup-page" />,
}))

const mockStatus = vi.fn()
const mockMe = vi.fn()

vi.mock('@/lib/api', () => ({
  setAuthCallbacks: vi.fn(),
  authApi: {
    status: (...args: unknown[]) => mockStatus(...args),
    me: (...args: unknown[]) => mockMe(...args),
  },
}))

// The budget this file asserts against, deliberately stated as one number in
// one place. It is comfortably above the ~50s worst case and far below the
// ~720s the unbounded loops cost, so it discriminates between them without
// pinning the exact per-attempt value -- lowering BOOT_PROBE_TIMEOUT_MS must
// not break this test, only raising it past ~20s should.
const BOOT_BUDGET_MS = 60_000

// A probe that answers, slowly, but inside any plausible per-attempt budget.
const answersSlowly = <T,>(value: T, afterMs: number) => () =>
  new Promise<T>((resolve) => setTimeout(() => resolve(value), afterMs))

// The failure this bead is about: the connection is accepted and then nothing
// ever comes back. Not a rejection -- a promise that never settles.
const neverAnswers = () => new Promise<never>(() => {})

const testUser: User = { id: 'u1', username: 'someone', createdAt: '2026-01-01T00:00:00Z' }

describe('App boot probes are bounded per attempt when the backend accepts but never answers', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    useAuthStore.setState({
      token: null,
      user: null,
      isAuthenticated: false,
      authDisabled: false,
      needsSetup: false,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('still uses the answer from a backend that is slow but inside the per-attempt budget', async () => {
    // The arm that makes the hang arm mean something. 5s is slow enough that an
    // over-aggressive timeout would cut it off, and inside the 8s budget.
    mockStatus.mockImplementation(answersSlowly({ authDisabled: false, needsSetup: false }, 5000))
    mockMe.mockImplementation(answersSlowly(testUser, 5000))

    renderWithProviders(<App />, { route: '/' })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(BOOT_BUDGET_MS)
    })

    // Only a SUCCESSFUL /auth/me reaches this branch: a cut-off probe leaves
    // isAuthenticated false and renders the login page instead.
    expect(screen.getByTestId('app-shell')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard')).toBeInTheDocument()
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useAuthStore.getState().user).toEqual(testUser)
    // And it was not cut off and retried into a second answer either.
    expect(mockStatus).toHaveBeenCalledTimes(1)
    expect(mockMe).toHaveBeenCalledTimes(1)
  })

  it('reaches a rendered branch within the boot budget when both probes hang forever', async () => {
    mockStatus.mockImplementation(neverAnswers)
    mockMe.mockImplementation(neverAnswers)

    renderWithProviders(<App />, { route: '/' })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(BOOT_BUDGET_MS)
    })

    expect(screen.getByText('Enter your credentials to access Capstan')).toBeInTheDocument()
    // agent-os-6hux's fail-closed default survives a hang, exactly as it
    // survives a rejection: an unreadable probe never opens the app.
    expect(useAuthStore.getState().authDisabled).toBe(false)
    // Bounded, not merely eventual: a hang is a no-response failure, so both
    // loops spend their full retry budget and then stop. 1 initial + 2 retries.
    expect(mockStatus).toHaveBeenCalledTimes(3)
    expect(mockMe).toHaveBeenCalledTimes(3)
  })
})
