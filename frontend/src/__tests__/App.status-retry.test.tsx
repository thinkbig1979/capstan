import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// agent-os-a4eh. agent-os-6hux stopped a failed boot probe from stranding the app
// on its spinner, but it stopped there: the app reaches the LOGIN path and never
// leaves it. checkStatus has one production call site (App.tsx:82) inside an
// effect whose deps are stable Zustand actions, so nothing re-probes for the life
// of the page. Two shapes are permanently wrong until a manual reload -- a fresh
// install never learns needsSetup, so /setup is unreachable and the first account
// cannot be created; and an AUTH_DISABLED deployment shows a login form for a
// deployment whose whole point is that there is no login.
//
// Every assertion below is on the RENDERED BRANCH, never on the absence of the
// spinner. 'Loading...' appears in five shipping components, and an absence
// assertion also passes when the render died or the ErrorBoundary caught -- it
// would prove the app is not on the spinner, not that it arrived anywhere.
//
// Two-sided on one instrument, because a retry that always fires proves nothing
// about the failure path: the happy-path arm pins that a first-attempt success
// issues EXACTLY ONE request, so a fix that simply probes twice always would fail
// it. The exhaustion arm pins that 6hux's fail-closed defaults still hold when
// every attempt fails.
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

describe('App boot status probe recovers from a transient failure', () => {
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

  it('reaches /setup when the first probe fails and a later one reports needsSetup', async () => {
    // The fresh-install shape: without a retry the app renders a login form for
    // an account that does not exist yet, and /setup is unreachable because
    // path="*" redirects to /login.
    mockStatus
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValue({ authDisabled: false, needsSetup: true })
    mockMe.mockRejectedValue(new Error('Unauthorized'))

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('setup-page', {}, { timeout: 5000 })).toBeInTheDocument()
    expect(mockStatus.mock.calls.length).toBeGreaterThan(1)
  })

  it('reaches the auth-disabled shell when the first probe fails and a later one reports authDisabled', async () => {
    // The AUTH_DISABLED shape: without a retry the user is shown a login form for
    // a deployment that has no login.
    mockStatus
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValue({ authDisabled: true, needsSetup: false })
    mockMe.mockRejectedValue(new Error('Unauthorized'))

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('app-shell', {}, { timeout: 5000 })).toBeInTheDocument()
  })

  it('issues exactly one status request when the first probe succeeds', async () => {
    // The control. A fix that retried unconditionally would pass both arms above
    // and fail this one, so this is what makes those two mean something.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: true })
    mockMe.mockRejectedValue(new Error('Unauthorized'))

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('setup-page', {}, { timeout: 5000 })).toBeInTheDocument()
    // Settle any retry that a broken fix would have scheduled before counting.
    await new Promise((resolve) => setTimeout(resolve, 1500))
    expect(mockStatus).toHaveBeenCalledTimes(1)
  })

  it('still falls back to the login path with fail-closed defaults when every attempt fails', async () => {
    // agent-os-6hux's guarantee must survive the retry: authDisabled is forced
    // back to false so a dead network can never open the app, and the user lands
    // somewhere real rather than on the spinner.
    mockStatus.mockRejectedValue(new Error('Network error'))
    mockMe.mockRejectedValue(new Error('Unauthorized'))

    renderWithProviders(<App />, { route: '/' })

    expect(
      await screen.findByText('Enter your credentials to access Capstan', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
    expect(useAuthStore.getState().authDisabled).toBe(false)
  })
})
