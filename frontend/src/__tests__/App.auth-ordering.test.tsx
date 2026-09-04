import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { App } from '../App'

// Pins the ORDER in which AuthenticatedLayout composes AuthGuard and AppShell.
//
// AppShell used to render OUTSIDE AuthGuard, so on a logged-out visit to `/` the
// shell mounted and fired all five of its queries (Sidebar's four via
// useSidebarData, plus /api/v1/dashboard/stats from HeaderVitals inside Header)
// against a dead session, before the guard could redirect to /login. Measured in
// a browser 2026-09-02 against a production build: 5 unauthorized requests before
// the redirect, 0 after the nesting was swapped. HTTP only -- the shell's two
// WebSockets were already self-guarded at useWebSocket.ts:92 and never connected
// without a session either way.
//
// This is worth a test because the bug was completely silent. It threw nothing,
// logged nothing, and showed no visible symptom -- the redirect still happened and
// the login page still appeared. The only thing that ever revealed it was someone
// counting requests in a real browser, and nothing in CI would notice it coming
// back. A regression here is a one-line reordering away at any time.
//
// Why the simple nesting swap is safe, rather than trading one bug for another:
// moving the shell inside the guard means a canAccess true->false transition now
// UNMOUNTS the shell, which would refire all five queries and discard shell state
// on any flicker. It cannot flicker. Established 2026-09-02 by enumerating the
// writes rather than reasoning about React: isAuthenticated has five write sites
// in stores/authStore.ts (:30 login, :40 setup, :58 logout, :69/:76 checkAuth)
// and authDisabled two, both inside checkStatus (:85 the probe's answer, :103 the
// restrictive fallback taken when the probe fails, which writes false and so can
// only lower access, never raise it); statusChecked holds a full-page spinner
// until both auth probes resolve, so nothing under AuthenticatedLayout renders
// with auth unresolved; login/setup live on route trees that never mount AppShell;
// the 401 interceptor navigates the document instead of mutating the store; and
// authStore has no persist middleware, so there is no rehydration flicker. The
// only reachable true->false while the shell is mounted is logout, which routes to
// /login in the next commit and destroyed the shell under the old nesting too.
// The behavioural delta of the swap is one React commit wide.
const { appShellMounts } = vi.hoisted(() => ({ appShellMounts: vi.fn() }))

vi.mock('@/components/layout/AppShell', () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => {
    appShellMounts()
    return <div data-testid="app-shell">{children}</div>
  },
}))

const mockUseAuth = {
  authDisabled: false,
  needsSetup: false,
  isAuthenticated: false,
  canAccess: false,
  checkAuth: vi.fn().mockResolvedValue(undefined),
  checkStatus: vi.fn().mockResolvedValue(undefined),
}

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => mockUseAuth,
}))

vi.mock('@/lib/api', () => ({
  setAuthCallbacks: vi.fn(),
}))

describe('AuthenticatedLayout query ordering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseAuth.authDisabled = false
    mockUseAuth.needsSetup = false
    mockUseAuth.checkAuth = vi.fn().mockResolvedValue(undefined)
    mockUseAuth.checkStatus = vi.fn().mockResolvedValue(undefined)
  })

  it('never mounts AppShell on a guarded route when access is denied', async () => {
    mockUseAuth.isAuthenticated = false
    mockUseAuth.canAccess = false

    renderWithProviders(<App />, { route: '/' })

    // The redirect target having rendered is the proof that the app got far
    // enough to mount the shell if it were going to -- without it, an absent
    // shell would only mean the render died somewhere earlier.
    expect(
      await screen.findByText('Enter your credentials to access Capstan'),
    ).toBeInTheDocument()
    // The mount spy is what actually catches the bug. The DOM check below is
    // NOT load-bearing -- verified 2026-09-02 that it passes under the broken
    // nesting too, because the redirect unmounts the shell before the assertion
    // runs. Deleting the spy would leave a test that passes either way.
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
    expect(appShellMounts).not.toHaveBeenCalled()
  })

  it('mounts AppShell on a guarded route once access is granted', async () => {
    mockUseAuth.isAuthenticated = true
    mockUseAuth.canAccess = true

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('app-shell')).toBeInTheDocument()
    expect(appShellMounts).toHaveBeenCalled()
  })
})
