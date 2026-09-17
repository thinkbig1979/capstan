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
  /**
   * agent-os-rtn8. `GetLog` reaches handleError from two places
   * (handlers/git.go's GetLog), and services/git.go mints three DISTINCT 404s
   * that land there: GIT_NOT_REPO "Not a git repository" (services/git.go:782),
   * GIT_NO_COMMITS "Repository has no commits yet" (services/git.go:118) and
   * STACK_DIR_MISSING "Stack directory does not exist on disk"
   * (services/git.go:769). They ask for three different operator actions --
   * init a repo, make a commit, fix the path -- and this branch rendered one
   * sentence for all three.
   *
   * The fixture is the FLAT shape api.ts's interceptor rejects with
   * (api.ts:127-131), which is what actually reaches the component.
   */
  it.each([
    ['GIT_NOT_REPO', 'Not a git repository'],
    ['GIT_NO_COMMITS', 'Repository has no commits yet'],
    ['STACK_DIR_MISSING', 'Stack directory does not exist on disk'],
  ])('names the cause the backend sent for a %s failure', (code, message) => {
    mockUseGitLog.mockReturnValue({
      isLoading: false,
      error: { status: 404, code, message },
      data: undefined,
    })

    renderWithProviders(<GitHistory stackId="stacks~myapp:default" />)

    expect(screen.getByText('Failed to load git history')).toBeInTheDocument()
    expect(screen.getByText(message)).toBeInTheDocument()
  })

  /**
   * The other side of the same instrument (agent-os-zlw0 criterion 2): the
   * fixed sentence is the HEADLINE and stays put, so a failure the backend
   * said nothing useful about renders what it rendered before the cause was
   * ever added. Without this arm the pair above is satisfied by a component
   * that replaced the headline rather than adding to it.
   */
  it('keeps the fixed sentence as the headline when the backend named no cause', () => {
    mockUseGitLog.mockReturnValue({
      isLoading: false,
      error: {},
      data: undefined,
    })

    renderWithProviders(<GitHistory stackId="stacks~myapp:default" />)

    expect(screen.getByText('Failed to load git history')).toBeInTheDocument()
    expect(screen.queryByText('Not a git repository')).not.toBeInTheDocument()
  })
})
