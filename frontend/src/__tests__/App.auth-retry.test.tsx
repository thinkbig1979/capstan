import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { useAuthStore } from '@/stores/authStore'
import { App } from '../App'

// agent-os-2cp3. checkAuth has one production call site (App.tsx's mount
// effect), whose deps are stable Zustand actions, so nothing re-probes for
// the life of the page (same shape as agent-os-a4eh's checkStatus fix,
// authStore.ts:80-148). Its catch used to unconditionally clear token/user/
// isAuthenticated on ANY authApi.me() rejection, so a user with a perfectly
// valid cookie session, whose single GET /auth/me happens to hit a transient
// network blip, was shown the login page and stayed there until a manual
// reload.
//
// A first version of this fix retried on every rejection, including a 401.
// That is wrong: /auth/me is behind AuthMiddleware, so every anonymous boot
// -- the common case, not an edge case -- gets a genuine 401 SESSION_EXPIRED.
// A 401 is a definitive answer already given; retrying it cannot change the
// outcome, only delay the login page by up to a second for every logged-out
// visitor. Only a no-response failure (network error, timeout) is worth a
// retry -- see authStore.ts's checkAuth comment for the discriminator.
//
// Every assertion below is on the RENDERED BRANCH, never on the absence of
// the spinner. 'Loading...' appears in five shipping components, and an
// absence assertion also passes when the render died or the ErrorBoundary
// caught -- it would prove the app is not on the spinner, not that it
// arrived anywhere.
//
// Two-sided on one instrument, now with two load-bearing controls: "issues
// exactly one auth check when the first probe succeeds" pins that a fix
// which always retries would still fail, and "issues exactly one auth check
// on a 401" pins that a fix which retries on ANY rejection (the bug this
// file was written to catch) also fails. The fail-closed arm pins that
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

const LOGIN_DESCRIPTION = 'Enter your credentials to access Capstan'
const SESSION_ERROR_TEXT = /Could not load your session/

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
    // asks again. Deliberately no-response-shaped (a plain Error has no
    // `.status`) so this exercises the network/timeout retry path, not the
    // "server already answered" short-circuit covered below.
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

  it('fails closed to the session error screen, not the login form, when every attempt gets no response', async () => {
    // agent-os-kd68 changed this arm. It used to expect the login form, but a
    // login form cannot work against a backend that does not answer, and
    // showing it tells a user with a valid session that they are logged out.
    // agent-os-6hux's guarantee still holds and is still asserted: never stuck
    // on the spinner, never granted access it didn't earn (no shell,
    // isAuthenticated false). Deliberately no-response-shaped (a plain Error
    // has no `.status`), so this exhausts the retry loop: 3 calls.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
    mockMe.mockRejectedValue(new Error('Network error'))

    renderWithProviders(<App />, { route: '/' })

    expect(
      await screen.findByText(SESSION_ERROR_TEXT, {}, { timeout: 3000 }),
    ).toBeInTheDocument()
    expect(screen.queryByText(LOGIN_DESCRIPTION)).not.toBeInTheDocument()
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
    expect(mockMe).toHaveBeenCalledTimes(3)
  })

  it('issues exactly one auth check on a 401, and does not delay reaching the login path', async () => {
    // The load-bearing arm this file was rewritten for. /auth/me is behind
    // AuthMiddleware (api.ts:106-110), so every anonymous boot -- the common
    // path, not an edge case -- gets a genuine 401 SESSION_EXPIRED. That is a
    // definitive answer from the server; retrying it cannot change the
    // outcome, only cost two extra requests and delay the login page by up
    // to a second for every logged-out visitor. A fix that retries on ANY
    // rejection (the original defect in this file) passes every other arm
    // here and fails this one.
    //
    // Shaped exactly as api.ts's response interceptor rejects a request that
    // got a response: a flat object carrying `status` (error-handler.ts:70's
    // convention), not a nested AxiosError.
    mockStatus.mockResolvedValue({ authDisabled: false, needsSetup: false })
    mockMe.mockRejectedValue({
      error: 'Unauthorized',
      code: 'SESSION_EXPIRED',
      message: 'Session expired',
      status: 401,
    })

    renderWithProviders(<App />, { route: '/' })

    expect(
      await screen.findByText('Enter your credentials to access Capstan', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
    // Settle any retry that a broken fix would have scheduled before counting.
    await new Promise((resolve) => setTimeout(resolve, 1500))
    expect(mockMe).toHaveBeenCalledTimes(1)
  })
})

// agent-os-kd68. checkAuth used to treat ANY rejection carrying a status as
// "no session": a 429 (RateLimitByUser on the protected group) or a 5xx (a
// backend restarting) at page load logged out a user whose cookie was still
// valid. Only a 401 means "no valid session". 429 and 5xx are retried like a
// no-response failure, and if they persist the app shows an error screen,
// failing closed: no protected page, and no login form claiming the user is
// logged out. Keyed on the rendered DOM, not on a store field, so the RED on
// the pre-fix code is an assertion failure, not a missing export.
describe('App boot auth probe on a non-auth HTTP failure (429 / 5xx)', () => {
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

  it.each([
    [429, 'RATE_LIMITED'],
    [503, 'SERVICE_UNAVAILABLE'],
  ])('retries a %i and reaches the shell when a later probe succeeds', async (status, code) => {
    mockMe
      .mockRejectedValueOnce({ error: 'failed', code, message: 'failed', status })
      .mockResolvedValue({ id: '1', username: 'edwin' })

    renderWithProviders(<App />, { route: '/' })

    expect(await screen.findByTestId('app-shell', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByText(LOGIN_DESCRIPTION)).not.toBeInTheDocument()
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(mockMe).toHaveBeenCalledTimes(2)
  })

  it('shows the session error screen, not the login form or the shell, when a 503 persists', async () => {
    mockMe.mockRejectedValue({
      error: 'Service Unavailable',
      code: 'SERVICE_UNAVAILABLE',
      message: 'Service Unavailable',
      status: 503,
    })

    renderWithProviders(<App />, { route: '/' })

    expect(
      await screen.findByText(SESSION_ERROR_TEXT, {}, { timeout: 3000 }),
    ).toBeInTheDocument()
    expect(screen.queryByText(LOGIN_DESCRIPTION)).not.toBeInTheDocument()
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
    expect(mockMe).toHaveBeenCalledTimes(3)
  })

  it('Retry on the session error screen re-probes and reaches the shell once /auth/me answers', async () => {
    mockMe
      .mockRejectedValueOnce({ status: 503, message: 'down' })
      .mockRejectedValueOnce({ status: 503, message: 'down' })
      .mockRejectedValueOnce({ status: 503, message: 'down' })
      .mockResolvedValue({ id: '1', username: 'edwin' })

    renderWithProviders(<App />, { route: '/' })

    await screen.findByText(SESSION_ERROR_TEXT, {}, { timeout: 3000 })
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('app-shell', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(mockMe).toHaveBeenCalledTimes(4)
  })
})
