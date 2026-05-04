import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { App } from '../App'

const mockUseAuth = {
  authDisabled: false,
  needsSetup: false,
  isAuthenticated: true,
  canAccess: true,
  checkAuth: vi.fn().mockResolvedValue(undefined),
  checkStatus: vi.fn(),
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
  })

  it('shows loading spinner before status check resolves', () => {
    mockUseAuth.checkStatus = vi.fn(() => new Promise(() => {}))
    render(<App />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })
})
