import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useAuthStore } from '../authStore'

const mockLogin = vi.fn()
const mockSetup = vi.fn()
const mockLogout = vi.fn()
const mockMe = vi.fn()
const mockStatus = vi.fn()

vi.mock('@/lib/api', () => ({
  authApi: {
    login: (...args: unknown[]) => mockLogin(...args),
    setup: (...args: unknown[]) => mockSetup(...args),
    logout: (...args: unknown[]) => mockLogout(...args),
    me: (...args: unknown[]) => mockMe(...args),
    status: (...args: unknown[]) => mockStatus(...args),
  },
}))

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

describe('authStore initial state', () => {
  it('has null token', () => {
    expect(useAuthStore.getState().token).toBeNull()
  })

  it('is not authenticated', () => {
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
  })
})

describe('authStore login', () => {
  it('sets token, user, and isAuthenticated on success', async () => {
    const mockUser = { id: '1', username: 'admin' }
    mockLogin.mockResolvedValue({ token: 'jwt-token-123', user: mockUser })

    await useAuthStore.getState().login('admin', 'password')

    const state = useAuthStore.getState()
    expect(state.token).toBe('jwt-token-123')
    expect(state.user).toEqual(mockUser)
    expect(state.isAuthenticated).toBe(true)
  })

  it('propagates login errors', async () => {
    mockLogin.mockRejectedValue(new Error('Invalid credentials'))

    await expect(useAuthStore.getState().login('admin', 'wrong')).rejects.toThrow('Invalid credentials')

    const state = useAuthStore.getState()
    expect(state.isAuthenticated).toBe(false)
  })
})

describe('authStore setup', () => {
  it('sets token, user, and clears needsSetup', async () => {
    const mockUser = { id: '1', username: 'newadmin' }
    mockSetup.mockResolvedValue({ token: 'setup-token', user: mockUser })

    useAuthStore.setState({ needsSetup: true })
    await useAuthStore.getState().setup('newadmin', 'password123')

    const state = useAuthStore.getState()
    expect(state.token).toBe('setup-token')
    expect(state.user).toEqual(mockUser)
    expect(state.isAuthenticated).toBe(true)
    expect(state.needsSetup).toBe(false)
  })
})

describe('authStore logout', () => {
  it('clears token, user, and isAuthenticated', async () => {
    useAuthStore.setState({
      token: 'existing-token',
      user: { id: '1', username: 'admin' },
      isAuthenticated: true,
    })

    mockLogout.mockResolvedValue(undefined)
    await useAuthStore.getState().logout()

    const state = useAuthStore.getState()
    expect(state.token).toBeNull()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
  })

  it('clears state even when logout API call fails', async () => {
    useAuthStore.setState({
      token: 'existing-token',
      user: { id: '1', username: 'admin' },
      isAuthenticated: true,
    })

    mockLogout.mockRejectedValue(new Error('Network error'))
    await useAuthStore.getState().logout()

    const state = useAuthStore.getState()
    expect(state.token).toBeNull()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
  })
})

describe('authStore checkAuth', () => {
  it('sets authenticated state on success', async () => {
    const mockUser = { id: '1', username: 'admin' }
    mockMe.mockResolvedValue(mockUser)

    await useAuthStore.getState().checkAuth()

    const state = useAuthStore.getState()
    expect(state.token).toBe('cookie')
    expect(state.user).toEqual(mockUser)
    expect(state.isAuthenticated).toBe(true)
  })

  it('clears auth state on failure', async () => {
    useAuthStore.setState({
      token: 'old-token',
      user: { id: '1', username: 'admin' },
      isAuthenticated: true,
    })

    mockMe.mockRejectedValue(new Error('Unauthorized'))
    await useAuthStore.getState().checkAuth()

    const state = useAuthStore.getState()
    expect(state.token).toBeNull()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
  })
})

describe('authStore checkStatus', () => {
  it('sets authDisabled and needsSetup from status', async () => {
    mockStatus.mockResolvedValue({ authDisabled: true, needsSetup: true })

    await useAuthStore.getState().checkStatus()

    const state = useAuthStore.getState()
    expect(state.authDisabled).toBe(true)
    expect(state.needsSetup).toBe(true)
  })

  it('resolves rather than rejecting when the status probe fails', async () => {
    mockStatus.mockRejectedValue(new Error('Network error'))

    // App.tsx awaits this in its boot effect. A rejection there skipped
    // setStatusChecked(true) and left the whole app on its loading spinner.
    await expect(useAuthStore.getState().checkStatus()).resolves.toBeUndefined()
  })

  it('forces authDisabled false but preserves needsSetup when the probe fails', async () => {
    // Seeding both true is what makes this discriminate: the store's initial
    // state is already authDisabled false / needsSetup false (authStore.ts:21-22),
    // so against the defaults this would pass with the catch's set() deleted.
    useAuthStore.setState({ authDisabled: true, needsSetup: true })
    mockStatus.mockRejectedValue(new Error('Network error'))

    await useAuthStore.getState().checkStatus()

    const state = useAuthStore.getState()
    // Forced: authDisabled alone satisfies useAuth's canAccess, so a failed
    // probe must not be able to leave it granting access.
    expect(state.authDisabled).toBe(false)
    // Preserved: a probe that failed learned nothing, and needsSetup routes to
    // /setup rather than granting access, so there is nothing to defend by
    // overwriting a value an earlier successful probe established.
    expect(state.needsSetup).toBe(true)
  })
})
