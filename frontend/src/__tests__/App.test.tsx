import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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
})
