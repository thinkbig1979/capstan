import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { App } from '../App'

const mockUseAuth = {
  authDisabled: false,
  needsSetup: false,
  isAuthenticated: true,
  canAccess: true,
  checkAuth: vi.fn().mockResolvedValue(undefined),
  checkStatus: vi.fn().mockResolvedValue(undefined),
}

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => mockUseAuth,
}))

vi.mock('@/lib/api', () => ({
  setAuthCallbacks: vi.fn(),
}))

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseAuth.isAuthenticated = true
    mockUseAuth.canAccess = true
    mockUseAuth.checkStatus = vi.fn().mockResolvedValue(undefined)
    mockUseAuth.checkAuth = vi.fn().mockResolvedValue(undefined)
  })

  it('shows loading spinner before status check resolves', () => {
    mockUseAuth.checkStatus = vi.fn(() => new Promise(() => {}))
    render(<App />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  it('rehydrates session on mount by calling checkAuth after checkStatus', async () => {
    render(<App />)
    await waitFor(() => {
      expect(mockUseAuth.checkStatus).toHaveBeenCalledTimes(1)
      expect(mockUseAuth.checkAuth).toHaveBeenCalledTimes(1)
    })
  })

  // Pins the boot effect's try/catch/finally independently of the store's own
  // error handling. checkStatus and checkAuth both swallow their failures
  // today, so with the real store nothing in init() can reject and reverting
  // App.tsx's guard alone leaves the whole suite green -- the guard exists for
  // a FUTURE await that rejects, and this injects that await now by rejecting
  // from the hook boundary. Without it the App half of the fix ships unpinned.
  it('clears the spinner when an auth probe rejects at the hook boundary', async () => {
    mockUseAuth.isAuthenticated = false
    mockUseAuth.canAccess = false
    mockUseAuth.checkAuth = vi.fn().mockRejectedValue(new Error('Network error'))

    renderWithProviders(<App />, { route: '/' })

    // Positive anchor first: reaching the login page is what proves the app got
    // somewhere. The spinner check below only names the symptom on failure.
    expect(
      await screen.findByText('Enter your credentials to access Capstan'),
    ).toBeInTheDocument()
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
  })
})
