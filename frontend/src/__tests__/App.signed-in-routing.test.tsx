import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// agent-os-d3yd. With auth on, the catch-all route sent EVERY unknown path to
// /login, signed in or not, and /login rendered the form with no check of the
// session. So a signed-in user who opened a typo or an old bookmark landed on
// the login form although /auth/me had just answered 200. The auth-disabled
// tree sends unknown paths to "/", which is why CI (AUTH_DISABLED=true) never
// saw it.
//
// Assertions are on the rendered branch and the final URL. The signed-out arm
// is the control: a fix that sent everyone to "/" passes the first two arms
// and fails it.
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

const LOGIN_DESCRIPTION = 'Enter your credentials to access Capstan'

describe('App routing for a signed-in user (auth on)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({
      token: null,
      user: null,
      isAuthenticated: false,
      authDisabled: false,
      needsSetup: false,
    })
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
  })

  it('sends a signed-in user on an unknown path to the dashboard, not /login', async () => {
    mockMe.mockResolvedValue({ id: '1', username: 'edwin' })

    renderWithProviders(<App />, { route: '/no-such-page' })

    expect(await screen.findByTestId('dashboard', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByText(LOGIN_DESCRIPTION)).not.toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
  })

  it('sends a signed-in user who opens /login on to the dashboard', async () => {
    mockMe.mockResolvedValue({ id: '1', username: 'edwin' })

    renderWithProviders(<App />, { route: '/login' })

    expect(await screen.findByTestId('dashboard', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByText(LOGIN_DESCRIPTION)).not.toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
  })

  it('still sends a signed-out user on an unknown path to /login', async () => {
    mockMe.mockRejectedValue({ status: 401, code: 'SESSION_EXPIRED', message: 'Session expired' })

    renderWithProviders(<App />, { route: '/no-such-page' })

    expect(await screen.findByText(LOGIN_DESCRIPTION, {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
    expect(window.location.pathname).toBe('/login')
  })
})
