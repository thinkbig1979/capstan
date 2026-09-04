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
// and authDisabled two, both inside checkStatus (:85 the probe's answer, :118 the
// restrictive fallback taken when the probe fails, added 2026-09-04). The second
// one is safe on REACHABILITY, not on direction: it writes false, which in this
// docblock's terms is the HAZARDOUS direction, since a canAccess true->false is
// exactly what unmounts the shell. What makes it harmless is that it fires inside
// App's boot effect while statusChecked is still false, when the full-page spinner
// is the whole tree and there is no mounted AuthenticatedLayout to unmount.
//
// That reachability argument holds for a SINGLE boot invocation. It used to NOT
// hold under StrictMode's dev double-invoke (main.tsx:8), where two init()s can
// be in flight: App's `ignore` flag guarded only setStatusChecked, not the store
// writes, so a late-failing first probe could write authDisabled false after a
// successful second probe had already mounted the shell -- DEV ONLY (StrictMode
// never double-invokes in a production build), and only when one probe failed
// while the other succeeded. Fixed by agent-os-lqsa: checkStatus/checkAuth
// (authStore.ts) now de-duplicate concurrent calls into a single shared
// in-flight request, so StrictMode's two invocations always observe the same
// outcome and there is no second invocation's result left to race against.
// Exercised directly in App.strictmode-race.test.tsx, not here -- this file
// mocks `@/hooks/useAuth` wholesale (below) and never touches the real store,
// so it cannot see this hazard either way, fixed or not.
//
// statusChecked holds a full-page spinner
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
