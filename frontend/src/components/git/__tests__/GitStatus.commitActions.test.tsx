/**
 * The git panel's "Commit and push" and "Push" buttons (agent-os-k81p.3,
 * review R3 N6). They come from GET /git/changes, not from the status: the
 * status's `dirty` counts the whole repository.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

const mockUseGitStatus = vi.fn()
const mockUseGitChanges = vi.fn()

vi.mock('@/hooks/useGit', () => ({
  useGitStatus: (...args: unknown[]) => mockUseGitStatus(...args),
  useGitPull: () => ({ mutate: vi.fn(), isPending: false }),
  useGitChanges: (...args: unknown[]) => mockUseGitChanges(...args),
}))

// The dialog has its own tests; here only whether it opens.
vi.mock('@/components/git/CommitPushDialog', () => ({
  CommitPushDialog: ({ stackId }: { stackId: string }) => <div role="dialog" aria-label={`commit dialog for ${stackId}`} />,
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/lib/api', () => ({
  directoriesApi: { list: vi.fn().mockResolvedValue([]), updateCredentials: vi.fn(), scan: vi.fn(), credentialStatus: vi.fn().mockResolvedValue({ status: 'none' }) },
}))

import { GitStatus } from '../GitStatus'

const stack = {
  envFile: '', gitBranch: '', gitCommit: '', containers: [], id: 's1', directory: '/opt/stacks/app',
  composeFile: 'compose.yaml', projectName: 'app', status: 'running' as const, isGitRepo: true,
  gitDirty: false, gitAhead: 0, gitBehind: 0,
}

function changes(over: Record<string, unknown> = {}) {
  return {
    branch: 'main', detached: false, trackingBranch: 'origin/main', hasUpstream: true, ahead: 0, behind: 0,
    pushEnabled: true, identityConfigured: true, changes: [], ...over,
  }
}

async function openPanel() {
  const user = userEvent.setup()
  renderWithProviders(<GitStatus stack={stack} />)
  await user.click(screen.getByRole('button', { name: /Git status/ }))
  return user
}

describe('GitStatus: commit and push buttons', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // The repository is dirty elsewhere (a sibling folder): not a reason to offer a commit here.
    mockUseGitStatus.mockReturnValue({
      error: null,
      data: {
        isRepo: true, hasCommits: true, isBare: false, branch: 'main', commit: 'abc', commitShort: 'abc1234',
        commitMessage: 'm', commitAuthor: 'a', commitDate: '2024-01-01', dirty: true, dirtyCount: 2, ahead: 0, behind: 0,
      },
    })
  })

  it('offers neither for a clean folder with nothing to push, whatever the repo-wide status says', async () => {
    mockUseGitChanges.mockReturnValue({ data: changes() })
    await openPanel()
    expect(await screen.findByRole('button', { name: /Git Pull/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Commit and push' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Push' })).not.toBeInTheDocument()
    expect(mockUseGitChanges).toHaveBeenCalledWith('s1')
  })

  it('offers Commit and push when the folder has changes, and it opens the dialog', async () => {
    mockUseGitChanges.mockReturnValue({ data: changes({ changes: [{ path: 'compose.yaml', status: '.M', isEnv: false }] }) })
    const user = await openPanel()
    expect(screen.queryByRole('button', { name: 'Push' })).not.toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'Commit and push' }))
    expect(screen.getByRole('dialog', { name: 'commit dialog for s1' })).toBeInTheDocument()
  })

  it('offers Push only with an upstream and commits ahead of it', async () => {
    mockUseGitChanges.mockReturnValue({ data: changes({ ahead: 2 }) })
    const user = await openPanel()
    await user.click(await screen.findByRole('button', { name: 'Push' }))
    expect(screen.getByRole('dialog', { name: 'commit dialog for s1' })).toBeInTheDocument()
  })

  it('ahead without an upstream offers no Push', async () => {
    mockUseGitChanges.mockReturnValue({ data: changes({ ahead: 2, hasUpstream: false }) })
    await openPanel()
    expect(await screen.findByRole('button', { name: /Git Pull/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Push' })).not.toBeInTheDocument()
  })
})
