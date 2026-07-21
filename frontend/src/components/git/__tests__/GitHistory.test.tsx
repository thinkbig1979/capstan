import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

const mockUseGitLog = vi.fn()

vi.mock('@/hooks/useGit', () => ({
  useGitLog: (...args: unknown[]) => mockUseGitLog(...args),
}))

import { GitHistory } from '../GitHistory'

const mockCommits = [
  {
    hash: 'abc123def456',
    short: 'abc123d',
    author: 'Jane Doe',
    email: 'jane@example.com',
    message: 'Fix container restart race condition',
    date: '2026-07-20T10:00:00Z',
  },
  {
    hash: 'def456abc789',
    short: 'def456a',
    author: 'John Smith',
    email: 'john@example.com',
    message: 'Add health check endpoint',
    date: '2026-07-19T09:00:00Z',
  },
]

describe('GitHistory', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders a paginated commit list', () => {
    mockUseGitLog.mockReturnValue({
      isLoading: false,
      error: null,
      data: { commits: mockCommits, total: 2, hasMore: false },
    })

    renderWithProviders(<GitHistory stackId="stacks~myapp:default" />)

    expect(screen.getByText('Fix container restart race condition')).toBeInTheDocument()
    expect(screen.getByText('Add health check endpoint')).toBeInTheDocument()
    expect(screen.getByText('abc123d')).toBeInTheDocument()
    expect(screen.getByText('Showing 2 commits')).toBeInTheDocument()
  })

  it('shows empty state when there are no commits', () => {
    mockUseGitLog.mockReturnValue({
      isLoading: false,
      error: null,
      data: { commits: [], total: 0, hasMore: false },
    })

    renderWithProviders(<GitHistory stackId="stacks~myapp:default" />)

    expect(screen.getByText('No commits in this repository')).toBeInTheDocument()
  })
})
