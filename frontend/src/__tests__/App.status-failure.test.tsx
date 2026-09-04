import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// A failing GET /auth/status used to strand the app on its full-page spinner for
// good. checkStatus had no try/catch, so the boot effect's init() rejected before
// it could set statusChecked, and App.tsx's !statusChecked branch is the spinner.
// Nothing recovered it: no retry, no timeout, no error state. A backend restart
// that overlapped a page load bricked that tab until the user reloaded by hand,
// and the only trace was an unhandled rejection in the console.
//
// The load-bearing assertion is the PRESENCE of the login page. Absence of
// 'Loading...' is satisfied by a crashed tree, by an empty DOM, and by the
// ErrorBoundary fallback (ErrorBoundary.tsx:126 renders 'Something went wrong',
// which contains no 'Loading...'), so on its own it proves only that the app is
// not on the spinner -- not that it got anywhere. The findByText anchor is what
// proves it reached a real destination. The absence check earns its place as a
// secondary: it is the string the stranded user is staring at, so when this test
// fails the message names the real symptom instead of reporting that some other
// element did not turn up.
//
// Both directions are tested on the same instrument: a rejecting probe must reach
// the login path, and a resolving one must still reach the authenticated shell.
// A fix that stranded the happy path would pass the first test alone.
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

describe('App boot when the status probe fails', () => {
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

  it('leaves the spinner for the login path when the status probe rejects', async () => {
    mockStatus.mockRejectedValue(new Error('Network error'))
    // agent-os-2cp3. Shaped as api.ts's interceptor actually rejects a request
    // that got a response (a flat object carrying `status`), not a bare
    // Error -- a real /auth/me rejection always carries one. This also
    // matters for timing: checkAuth only retries a NO-response failure
    // (authStore.ts's checkAuth comment), so a bare Error here used to make
    // it retry for another ~1s on top of checkStatus's own ~1s retry below,
    // pushing this test past the "Loading..." waitFor's budget.
    mockMe.mockRejectedValue({ status: 401, code: 'SESSION_EXPIRED', message: 'Session expired' })

    renderWithProviders(<App />, { route: '/' })

    await waitFor(() => expect(mockStatus).toHaveBeenCalled())
    // agent-os-2cp3 (pre-existing flake from agent-os-a4eh, fixed here). This
    // races checkStatus's own ~1s retry budget ([250, 750]ms) against RTL's
    // default 1000ms waitFor timeout -- marginal even before this file's
    // checkAuth fix touched anything else in it. Explicit headroom, not a
    // narrower fix, because checkStatus's retry window is deliberately sized
    // independently of this test.
    await waitFor(
      () => expect(screen.queryByText('Loading...')).not.toBeInTheDocument(),
      { timeout: 5000 },
    )
    expect(
      await screen.findByText('Enter your credentials to access Capstan'),
    ).toBeInTheDocument()

    // An unreadable probe must never open the shell: canAccess is
    // `authDisabled || isAuthenticated`, so authDisabled true on its own is a
    // fully accessible app with no session behind it.
    expect(useAuthStore.getState().authDisabled).toBe(false)
    expect(appShellMounts).not.toHaveBeenCalled()
  })

  it('still reaches the authenticated app when the status probe resolves', async () => {
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
    mockMe.mockResolvedValue({ id: '1', username: 'admin' })

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('app-shell')).toBeInTheDocument()
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    expect(appShellMounts).toHaveBeenCalled()
  })
})
