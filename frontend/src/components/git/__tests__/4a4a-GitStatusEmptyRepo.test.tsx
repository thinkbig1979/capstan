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

/**
 * The frontend half of agent-os-4a4a.
 *
 * A `git init`'d stack with no commits used to arrive as an ERROR (404
 * GIT_NO_COMMITS) and the component rendered nothing, which was the whole
 * symptom: a red failed request in the console for a state nobody did anything
 * wrong to reach. It now arrives as DATA, `{isRepo: true, hasCommits: false}`.
 *
 * Rendering nothing for it would have been the cheap answer and it is the wrong
 * one: an empty repository IS a repository, and the operator who ran `git init`
 * and expected to see git in the UI would see no git at all, with no way to
 * tell that state from "this directory has nothing to do with git". The chip
 * says which, and stays inert — there is no branch, no commit and no remote to
 * put behind a popover, and nothing to pull.
 *
 * The three cases below are one instrument on purpose. The first is the new
 * behaviour; the second is the control that forbids "render the chip for
 * anything that says isRepo", which would put an empty chip on every non-git
 * stack; the third is the control that forbids the opposite mistake, a guard
 * broad enough to swallow ordinary repositories.
 */
describe('GitStatus, repository with no commits (agent-os-4a4a)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: vi.fn(), isPending: false })
  })

  it('names the empty repository instead of rendering nothing', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { isRepo: true, hasCommits: false },
    })

    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.getByText('no commits')).toBeInTheDocument()
    // An empty repository IS a repository, so agent-os-omvy's non-repo chip
    // must not reach this case.
    expect(screen.queryByText('not a git repository')).not.toBeInTheDocument()
    // Inert by design: nothing to pull, no remote to configure, no commit to
    // show. A button here would open a popover with three empty rows. It also
    // forbids omvy's Rescan button on this branch.
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  // Updated by agent-os-omvy, which replaced the blank render for this payload
  // with a chip naming the state and offering Rescan. The control's job is
  // unchanged and is the reason it is asserted from both sides here: the two
  // negative answers must stay distinguishable. An empty repository says "no
  // commits"; a directory with no repository in it says so and offers the one
  // action that changes the answer.
  it('CONTROL says not-a-repository, not no-commits, for a directory that is not a repository', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: { isRepo: false },
    })

    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.getByText('not a git repository')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /rescan/i })).toBeInTheDocument()
    expect(screen.queryByText('no commits')).not.toBeInTheDocument()
  })

  it('CONTROL still renders the branch chip for a repository with commits', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: {
        isRepo: true,
        hasCommits: true,
        branch: 'main',
        commit: 'abc123',
        commitShort: 'abc1234',
        commitMessage: 'test',
        commitAuthor: 'test',
        commitDate: '2024-01-01',
        dirty: false,
        dirtyCount: 0,
        ahead: 0,
        behind: 0,
      },
    })

    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.getByText('main')).toBeInTheDocument()
    expect(screen.queryByText('no commits')).not.toBeInTheDocument()
    expect(screen.queryByText('not a git repository')).not.toBeInTheDocument()
  })
})
