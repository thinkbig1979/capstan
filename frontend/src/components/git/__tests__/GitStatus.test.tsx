import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

const mockUseGitStatus = vi.fn()
const mockUseGitPull = vi.fn(() => ({ mutate: vi.fn(), isPending: false }))

vi.mock('@/hooks/useGit', () => ({
  useGitStatus: (...args: unknown[]) => mockUseGitStatus(...args),
  useGitPull: () => mockUseGitPull(),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

import { GitStatus } from '../GitStatus'

describe('GitStatus', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: vi.fn(), isPending: false })
  })

  it('shows loading state', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: true, error: null, data: null })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('Loading git status...')).toBeInTheDocument()
  })

  it('shows error state when fetch fails', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: new Error('fail'), data: null })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('Failed to load git status')).toBeInTheDocument()
  })

  it('renders branch name', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', dirty: false, ahead: 0, behind: 0 },
    })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('main')).toBeInTheDocument()
  })

  it('shows ahead badge when ahead > 0', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'feature', commit: 'def456', dirty: false, ahead: 3, behind: 0 },
    })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('↑ 3')).toBeInTheDocument()
  })

  it('shows behind badge when behind > 0', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', dirty: false, ahead: 0, behind: 2 },
    })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('↓ 2')).toBeInTheDocument()
  })

  it('shows dirty badge when dirty is true', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', dirty: true, ahead: 0, behind: 0 },
    })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('dirty')).toBeInTheDocument()
  })

  it('renders pull buttons', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', dirty: false, ahead: 0, behind: 0 },
    })
    renderWithProviders(<GitStatus stackId="myapp:default" />)
    expect(screen.getByText('Pull')).toBeInTheDocument()
    expect(screen.getByText('Pull & Redeploy')).toBeInTheDocument()
  })
})
