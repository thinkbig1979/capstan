import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// agent-os-lqsa. DEV ONLY: main.tsx wraps <App/> in <StrictMode>, and
// StrictMode double-invokes dev mount effects (mount -> cleanup -> remount).
// This never happens in a production build.
//
// Before this fix, App's boot effect called checkStatus()/checkAuth()
// (authStore.ts) with no guard against being called twice concurrently, so
// StrictMode's double-invoke meant two independent network requests racing
// to `set()` the module-global authStore, with no ordering guarantee
// between them. A slow-failing first request could overwrite a
// fast-succeeding second one's result well after the app had already
// rendered based on the first answer to arrive -- e.g. a fast checkStatus
// resolving authDisabled: true (mounting AppShell via App.tsx's `if
// (authDisabled)` branch), then a slow one later resolving/rejecting and
// writing authDisabled: false, which flips `canAccess` (authDisabled ||
// isAuthenticated, useAuth.ts) true -> false with the shell mounted. App
// re-renders, the authDisabled branch is no longer taken, and the tree
// falls through to the final `<Routes>` block, whose `path="*"` redirects
// to /login -- unmounting the shell. (Not an AuthGuard unmount: AuthGuard
// isn't in the authDisabled branch's tree at all; the teardown is App's own
// top-level branch switch.)
//
// The fix (authStore.ts) de-duplicates concurrent calls into a single
// shared in-flight request per action: a call made while one is already
// in-flight returns that SAME promise instead of issuing a second one. Both
// StrictMode invocations then observe the identical outcome and there is
// exactly one `set()` per real probe -- there is no "other" invocation's
// result left over to race against.
//
// An earlier fix direction (drop whichever invocation's write is
// "superseded") was tried and rejected during review: checkStatus's failure
// path deliberately never writes needsSetup (see its own comment), so
// dropping a whole invocation's result can throw away a genuine
// needsSetup: true that only THAT invocation's successful probe learned --
// the second test below pins that.

const { appShellMounts } = vi.hoisted(() => ({ appShellMounts: vi.fn() }))

vi.mock('@/components/layout/AppShell', () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => {
    appShellMounts()
    return <div data-testid="app-shell">{children}</div>
  },
}))

vi.mock('@/pages/SetupPage', () => ({
  SetupPage: () => <div data-testid="setup-page" />,
}))

vi.mock('@/pages/DashboardPage', () => ({
  DashboardPage: () => <div data-testid="dashboard" />,
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

// A definitive server answer, never retried by checkAuth (authStore.ts's
// checkAuth only retries a no-response failure). Its outcome is irrelevant
// to every assertion here, so it is fixed to the same immediate 401 in
// every test to stay out of the way.
const SESSION_EXPIRED = { status: 401, code: 'SESSION_EXPIRED', message: 'Session expired' }

describe('App boot under StrictMode double-invoke', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({
      token: null,
      user: null,
      isAuthenticated: false,
      authDisabled: false,
      needsSetup: false,
    })
  })

  it('issues exactly one status/me request despite the double-invoke, and keeps the shell mounted', async () => {
    mockStatus.mockResolvedValue({ authDisabled: true, needsSetup: false })
    mockMe.mockRejectedValue(SESSION_EXPIRED)

    renderWithProviders(<App />, { route: '/', strictMode: true })

    expect(await screen.findByTestId('app-shell')).toBeInTheDocument()

    // Let both StrictMode invocations fully settle before counting -- a
    // broken fix could still schedule a second, later request. This also
    // gives the authDisabled branch's lazy DashboardPage chunk (App.tsx:15,
    // rendered at route "/") time to resolve and mount for real.
    await new Promise((resolve) => setTimeout(resolve, 300))

    // The load-bearing counts: without de-duplication, StrictMode's second
    // invocation issues its OWN independent request, so this reads 2 on
    // unfixed code.
    expect(mockStatus).toHaveBeenCalledTimes(1)
    expect(mockMe).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('app-shell')).toBeInTheDocument()
    expect(
      screen.queryByText('Enter your credentials to access Capstan'),
    ).not.toBeInTheDocument()
    // A crashed tree and a not-yet-rendered one both fail getByTestId the
    // same way (CI red 2026-09-04: an unmocked DashboardPage threw for
    // wanting DashboardMetricsProvider, ErrorBoundary swallowed it, and the
    // resulting fallback was indistinguishable from "shell not up yet"
    // until this line named it). Asserting the fallback's absence turns
    // that failure mode into an immediately legible one instead of a
    // flaky-looking "unable to find" timeout.
    expect(screen.queryByText('Something went wrong')).not.toBeInTheDocument()
  })

  it('does not lose a needsSetup: true answer to a discarded invocation -- reaches /setup, not /login', async () => {
    // checkStatus's failure path deliberately never writes needsSetup
    // (authStore.ts's own comment), so this is the case an "ignore the
    // superseded invocation" fix gets wrong: if the invocation carrying the
    // successful needsSetup: true were the one such a fix discards, a fresh
    // install would lose its only route to /setup. With a single shared
    // request there is no second invocation's answer to discard.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: true })
    mockMe.mockRejectedValue(SESSION_EXPIRED)

    renderWithProviders(<App />, { route: '/', strictMode: true })

    expect(await screen.findByTestId('setup-page')).toBeInTheDocument()
    expect(useAuthStore.getState().needsSetup).toBe(true)
    expect(mockStatus).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('Something went wrong')).not.toBeInTheDocument()
  })

  it('still reaches the login page, via one shared retry sequence, when the probe never succeeds', async () => {
    mockStatus.mockRejectedValue({ status: undefined })
    mockMe.mockRejectedValue(SESSION_EXPIRED)

    renderWithProviders(<App />, { route: '/', strictMode: true })

    await waitFor(
      () => expect(screen.queryByText('Loading...')).not.toBeInTheDocument(),
      { timeout: 5000 },
    )
    expect(
      await screen.findByText('Enter your credentials to access Capstan'),
    ).toBeInTheDocument()
    expect(useAuthStore.getState().authDisabled).toBe(false)
    expect(appShellMounts).not.toHaveBeenCalled()
    expect(screen.queryByText('Something went wrong')).not.toBeInTheDocument()
    // One shared retry sequence (checkStatus's own [250, 750]ms backoff, 3
    // attempts total) rather than one per StrictMode invocation (6 calls).
    expect(mockStatus).toHaveBeenCalledTimes(3)
  })
})
