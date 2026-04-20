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

vi.mock('@/lib/api', () => ({
  directoriesApi: {
    list: vi.fn().mockResolvedValue([]),
    updateCredentials: vi.fn(),
  },
}))

import { GitStatus } from '../GitStatus'

const mockStack = {
  id: 'myapp:default',
  directory: '/opt/stacks/myapp',
  composeFile: 'docker-compose.yaml',
  projectName: 'myapp',
  status: 'running' as const,
  isGitRepo: true,
  gitDirty: false,
  gitAhead: 0,
  gitBehind: 0,
}

describe('GitStatus', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: vi.fn(), isPending: false })
  })

  it('shows loading state', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: true, error: null, data: null })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('Loading git status...')).toBeInTheDocument()
  })

  it('shows not a git repo message when fetch fails', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: new Error('fail'), data: null })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('This directory is not a git repository.')).toBeInTheDocument()
  })

  it('renders branch name', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', commitShort: 'abc1234', commitMessage: 'test', commitAuthor: 'test', commitDate: '2024-01-01', dirty: false, dirtyCount: 0, ahead: 0, behind: 0 },
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('main')).toBeInTheDocument()
  })

  it('shows ahead badge when ahead > 0', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'feature', commit: 'def456', commitShort: 'def4567', commitMessage: 'test', commitAuthor: 'test', commitDate: '2024-01-01', dirty: false, dirtyCount: 0, ahead: 3, behind: 0 },
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('3 ahead')).toBeInTheDocument()
  })

  it('shows behind badge when behind > 0', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', commitShort: 'abc1234', commitMessage: 'test', commitAuthor: 'test', commitDate: '2024-01-01', dirty: false, dirtyCount: 0, ahead: 0, behind: 2 },
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('2 behind')).toBeInTheDocument()
  })

  it('shows dirty badge when dirty is true', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', commitShort: 'abc1234', commitMessage: 'test', commitAuthor: 'test', commitDate: '2024-01-01', dirty: true, dirtyCount: 3, ahead: 0, behind: 0 },
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('3 uncommitted changes')).toBeInTheDocument()
  })

  it('renders pull buttons', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { branch: 'main', commit: 'abc123', commitShort: 'abc1234', commitMessage: 'test', commitAuthor: 'test', commitDate: '2024-01-01', dirty: false, dirtyCount: 0, ahead: 0, behind: 0 },
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('Git Pull')).toBeInTheDocument()
    expect(screen.getByText('Pull & Redeploy')).toBeInTheDocument()
  })
})
