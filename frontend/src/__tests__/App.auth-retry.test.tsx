import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// agent-os-2cp3. checkAuth has one production call site (App.tsx's mount
// effect), whose deps are stable Zustand actions, so nothing re-probes for
// the life of the page (same shape as agent-os-a4eh's checkStatus fix,
// authStore.ts:80-148). Its catch unconditionally clears token/user/
// isAuthenticated on ANY authApi.me() rejection, so a user with a perfectly
// valid cookie session, whose single GET /auth/me happens to hit a transient
// blip, is shown the login page and stays there until a manual reload.
//
// Every assertion below is on the RENDERED BRANCH, never on the absence of
// the spinner. 'Loading...' appears in five shipping components, and an
// absence assertion also passes when the render died or the ErrorBoundary
// caught -- it would prove the app is not on the spinner, not that it
// arrived anywhere.
//
// Two-sided on one instrument: the control arm pins that a first-attempt
// success issues EXACTLY ONE request, so a fix that simply probes twice
// unconditionally would fail it. The fail-closed arm pins that
// agent-os-6hux's fail-closed defaults still hold when every attempt fails.
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

describe('App boot auth probe recovers from a transient failure', () => {
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

  it('reaches the authenticated shell when the first /auth/me fails and a later one succeeds', async () => {
    // Without a retry this valid session is stranded on /login: checkAuth's
    // catch clears isAuthenticated on the first rejection and nothing ever
    // asks again.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
    mockMe
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValue({ id: '1', username: 'edwin' })

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('app-shell', {}, { timeout: 5000 })).toBeInTheDocument()
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
  })

  it('issues exactly one auth check when the first probe succeeds', async () => {
    // The control. A fix that retried unconditionally would pass the arm
    // above and fail this one, so this is what makes that arm mean
    // something.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
    mockMe.mockResolvedValue({ id: '1', username: 'edwin' })

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('app-shell', {}, { timeout: 5000 })).toBeInTheDocument()
    // Settle any retry that a broken fix would have scheduled before counting.
    await new Promise((resolve) => setTimeout(resolve, 1500))
    expect(mockMe).toHaveBeenCalledTimes(1)
  })

  it('still falls back to the login path with fail-closed defaults when every attempt fails', async () => {
    // agent-os-6hux's guarantee must survive the retry: a permanently dead
    // /auth/me still lands the user on /login with isAuthenticated false,
    // never stuck on the spinner and never granted access it didn't earn.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
    mockMe.mockRejectedValue(new Error('Unauthorized'))

    renderWithProviders(<App />, { route: '/' })

    expect(
      await screen.findByText('Enter your credentials to access Capstan', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
  })
})
