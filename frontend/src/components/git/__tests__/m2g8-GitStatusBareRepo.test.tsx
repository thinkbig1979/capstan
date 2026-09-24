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
  envFile: '',
  gitBranch: '',
  gitCommit: '',
  containers: [],
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

// The body handlers/git.go sends for a bare repository: no work-tree fields.
const BARE = {
  isRepo: true,
  hasCommits: true,
  isBare: true,
  branch: 'main',
  commit: 'abc1234def',
  commitShort: 'abc1234',
  commitMessage: 'seed',
  commitAuthor: 't',
  commitDate: '2026-09-24',
  remote: '',
}

const REPO = {
  ...BARE,
  isBare: false,
  dirty: false,
  dirtyCount: 0,
  ahead: 0,
  behind: 0,
}

function hookState(state: { error?: unknown; data?: unknown }) {
  return { isLoading: false, error: null, data: undefined, refetch: vi.fn(), ...state }
}

/**
 * The frontend half of agent-os-m2g8.
 *
 * A bare repository used to answer 500, so the header showed "git status
 * unknown" for it permanently. It now answers 200 with a branch, a commit and
 * `isBare: true`, and no work-tree fields. Read as an ordinary repository, the
 * missing `dirty` renders "clean" and the missing `behind` hides the pull
 * signal, with Pull enabled on a repository nothing can be pulled into. So the
 * chip says "bare repo" and offers no Pull at all.
 */
describe('GitStatus, bare repository (agent-os-m2g8)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: vi.fn(), isPending: false })
  })

  it('names the bare repository with its branch and commit, and offers no Pull', () => {
    mockUseGitStatus.mockReturnValue(hookState({ data: BARE }))

    renderWithProviders(<GitStatus stack={mockStack} />)

    const chip = screen.getByLabelText('Git status: main, bare repository')
    expect(chip).toHaveTextContent('main')
    expect(chip).toHaveTextContent('abc1234')
    expect(chip).toHaveTextContent('bare repo')
    // Nothing a work tree would say, and nothing that opens onto Pull.
    expect(screen.queryByText(/clean|dirty|status unknown/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('still offers no Pull when a refetch over bare data fails', () => {
    mockUseGitStatus.mockReturnValue(hookState({ data: BARE, error: new Error('boom') }))

    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.getByLabelText('Git status: main, bare repository')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /pull/i })).not.toBeInTheDocument()
  })

  it('CONTROL renders the ordinary chip for a repository with a work tree', () => {
    mockUseGitStatus.mockReturnValue(hookState({ data: REPO }))

    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.getByRole('button', { name: 'Git status: main, clean' })).toBeInTheDocument()
    expect(screen.queryByText('bare repo')).not.toBeInTheDocument()
  })
})
