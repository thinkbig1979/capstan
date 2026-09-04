import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// agent-os-lqsa. main.tsx wraps <App/> in <StrictMode>, and StrictMode
// double-invokes dev mount effects: mount -> cleanup -> remount. App's boot
// effect (App.tsx) guards its OWN `ignore` flag around `setStatusChecked`
// only -- it does not guard the store writes inside checkStatus/checkAuth,
// which are unguarded `set()` calls on the module-global authStore.
//
// So with two overlapping invocations of the boot effect (call them A, the
// one StrictMode cleans up, and B, the one that survives), this interleaving
// was reachable:
//
//   B's checkStatus resolves fast with authDisabled: true -> shell mounts
//   A's checkStatus, still retrying, eventually exhausts its retries and
//     writes authDisabled: false -- LATE, after the shell already mounted
//   canAccess (authDisabled || isAuthenticated) flips true -> false with the
//     shell mounted -> AuthGuard's route branch swaps and the shell is
//     replaced by the login page
//
// That true->false-while-mounted transition is exactly what
// App.auth-ordering.test.tsx's docblock says is unreachable outside logout.
// It quietly stopped holding once StrictMode was added; this file exercises
// it directly rather than relying on that file's mocked-out useAuth to catch
// it (that file mocks `@/hooks/useAuth` wholesale and never touches the real
// store, so it cannot see this race either way).
//
// Both arms below are on the SAME instrument (authDisabled's final value and
// which page is on screen), which is what makes them meaningful together: a
// "fix" that just pins authDisabled true, or that ignores every late write
// including ones that SHOULD count, would pass the first arm and fail the
// second.

const { appShellMounts } = vi.hoisted(() => ({ appShellMounts: vi.fn() }))

vi.mock('@/components/layout/AppShell', () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => {
    appShellMounts()
    return <div data-testid="app-shell">{children}</div>
  },
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
// to every assertion here -- authDisabled alone satisfies canAccess -- so it
// is fixed to the same immediate 401 in every test to stay out of the way.
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

  it('keeps the shell mounted when the superseded invocation later fails, after the surviving one already succeeded', async () => {
    // Invocation A is the one StrictMode's synchronous cleanup runs first
    // (its effect setup executes before B's, so its first network call is
    // always issued first). It is held open with an externally-controlled
    // gate so the test decides exactly when it fails, strictly after B's
    // success has already reached the DOM.
    let releaseFirstAttempt: () => void = () => {}
    const firstAttemptGate = new Promise<void>((resolve) => {
      releaseFirstAttempt = resolve
    })

    let callIndex = 0
    mockStatus.mockImplementation(() => {
      callIndex += 1
      if (callIndex === 1) {
        // Invocation A's first attempt: held until the test releases it.
        return firstAttemptGate.then(() => Promise.reject({ status: undefined }))
      }
      if (callIndex === 2) {
        // Invocation B's only attempt: succeeds immediately.
        return Promise.resolve({ authDisabled: true, needsSetup: false })
      }
      // Invocation A's retries (checkStatus retries on ANY failure, per
      // authStore.ts). Both fail too, so A's eventual conclusion is a
      // genuine rejection, not a late lucky success.
      return Promise.reject({ status: undefined })
    })
    mockMe.mockRejectedValue(SESSION_EXPIRED)

    renderWithProviders(<App />, { route: '/', strictMode: true })

    // B has won: the shell is up, driven by authDisabled: true.
    expect(await screen.findByTestId('app-shell')).toBeInTheDocument()
    expect(useAuthStore.getState().authDisabled).toBe(true)

    // Now let A's held attempt fail, and let it exhaust its own retries
    // ([250, 750]ms, authStore.ts). Generous headroom past that ~1s budget,
    // matching this suite's existing convention for the same retry window
    // (App.status-failure.test.tsx).
    releaseFirstAttempt()

    // A settled window, not a positive condition to poll for: the whole
    // point is that NOTHING should change from here, so there is nothing to
    // waitFor. Sleeping past A's full retry budget and then asserting the
    // end state is the only way to see an absence of a late regression.
    await new Promise((resolve) => setTimeout(resolve, 1800))

    expect(useAuthStore.getState().authDisabled).toBe(true)
    expect(screen.getByTestId('app-shell')).toBeInTheDocument()
    expect(
      screen.queryByText('Enter your credentials to access Capstan'),
    ).not.toBeInTheDocument()
  }, 10000)

  it('still reaches the login page when both invocations fail', async () => {
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
  })

  it('mounts the shell exactly once when both invocations succeed', async () => {
    mockStatus.mockResolvedValue({ authDisabled: true, needsSetup: false })
    mockMe.mockRejectedValue(SESSION_EXPIRED)

    renderWithProviders(<App />, { route: '/', strictMode: true })

    expect(await screen.findByTestId('app-shell')).toBeInTheDocument()

    // Let both StrictMode invocations fully settle (each resolves on its
    // first attempt, no retries involved here -- this is well past that).
    await new Promise((resolve) => setTimeout(resolve, 200))

    expect(screen.getByTestId('app-shell')).toBeInTheDocument()
    expect(
      screen.queryByText('Enter your credentials to access Capstan'),
    ).not.toBeInTheDocument()
    // Exactly one status call per boot-effect invocation (StrictMode's A and
    // B) -- never a retry storm, and never a third invocation triggered by
    // the shell itself mounting.
    expect(mockStatus).toHaveBeenCalledTimes(2)
  })
})
