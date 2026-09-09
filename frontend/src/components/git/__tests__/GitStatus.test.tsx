import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    scan: vi.fn().mockResolvedValue({ directories: [], hasGlobalEnv: false, scannedAt: '' }),
  },
}))

import { directoriesApi } from '@/lib/api'
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

// Neither discriminator is decoration — the component narrows on both and
// renders nothing (isRepo) or the inert "no commits" chip (hasCommits) without
// them, so every row below would go empty or wrong if the backend ever stopped
// emitting one. The backend side is pinned by
// handlers/x40a_nonrepo_200_test.go arm 2 and
// handlers/4a4a_emptyrepo_200_test.go arm 4.
function gitData(overrides: Record<string, unknown> = {}) {
  return {
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
    ...overrides,
  }
}

describe('GitStatus', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: vi.fn(), isPending: false })
  })

  it('renders nothing while loading', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: true, error: null, data: null })
    const { container } = renderWithProviders(<GitStatus stack={mockStack} />)
    expect(container).toBeEmptyDOMElement()
  })

  // Renamed in agent-os-x40a. It never tested a non-git directory — it drives an
  // ERROR, and a non-git directory now arrives as DATA (200 `{isRepo: false}`,
  // see below). What it does test is still worth keeping: a genuinely failed
  // request renders nothing rather than a broken chip. The old name would have
  // sent the next reader looking for non-repo coverage and finding this.
  it('renders nothing when the status request fails', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: new Error('fail'), data: null })
    const { container } = renderWithProviders(<GitStatus stack={mockStack} />)
    expect(container).toBeEmptyDOMElement()
  })

  // agent-os-omvy. This REPLACES an assertion that the same payload renders
  // nothing (agent-os-x40a's frontend half, which stopped the component reading
  // `gitStatus.branch` off a non-repo payload). The guard is still there; what
  // changed is what it renders. Nothing was a dead end: a blank stack header
  // with no statement of why.
  it('names a non-repository directory and offers Rescan', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: { isRepo: false } })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('not a git repository')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /rescan/i })).toBeInTheDocument()
  })

  // The offer has to be the action, not a word. A label reading "Rescan" that
  // scans nothing is the same dead end with better wording. What it buys is
  // narrow and real: this chip renders when the live probe says no repository
  // while the cached `Stack.isGitRepo` behind the badges may still say yes, and
  // the scan is what rewrites that field.
  it('runs a directory rescan when Rescan is clicked', async () => {
    const user = userEvent.setup()
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: { isRepo: false } })
    renderWithProviders(<GitStatus stack={mockStack} />)

    await user.click(screen.getByRole('button', { name: /rescan/i }))

    await waitFor(() => expect(vi.mocked(directoriesApi.scan)).toHaveBeenCalledTimes(1))
  })

  // Also the control on agent-os-omvy's affordance: a real repository must keep
  // the branch chip and must not pick up the non-repo one.
  it('renders branch name in the chip', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: gitData() })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('main')).toBeInTheDocument()
    expect(screen.queryByText('not a git repository')).not.toBeInTheDocument()
  })

  it('shows a clean marker when the working tree is clean', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: gitData() })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText(/clean/)).toBeInTheDocument()
  })

  it('shows a dirty count in the chip when dirty', () => {
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: gitData({ dirty: true, dirtyCount: 3 }),
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText(/3 dirty/)).toBeInTheDocument()
  })

  it('shows ahead badge in the popover when ahead > 0', async () => {
    const user = userEvent.setup()
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: gitData({ branch: 'feature', ahead: 3 }),
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    await user.click(screen.getByRole('button', { name: /Git status/ }))
    expect(await screen.findByText('3 ahead')).toBeInTheDocument()
  })

  it('shows behind badge in the popover when behind > 0', async () => {
    const user = userEvent.setup()
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: gitData({ behind: 2 }),
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    await user.click(screen.getByRole('button', { name: /Git status/ }))
    expect(await screen.findByText('2 behind')).toBeInTheDocument()
  })

  it('shows dirty badge in the popover when dirty is true', async () => {
    const user = userEvent.setup()
    mockUseGitStatus.mockReturnValue({
      isLoading: false,
      error: null,
      data: gitData({ dirty: true, dirtyCount: 3 }),
    })
    renderWithProviders(<GitStatus stack={mockStack} />)
    await user.click(screen.getByRole('button', { name: /Git status/ }))
    expect(await screen.findByText('3 uncommitted changes')).toBeInTheDocument()
  })

  it('renders pull buttons in the popover', async () => {
    const user = userEvent.setup()
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: gitData() })
    renderWithProviders(<GitStatus stack={mockStack} />)
    await user.click(screen.getByRole('button', { name: /Git status/ }))
    expect(await screen.findByText('Git Pull')).toBeInTheDocument()
    expect(screen.getByText('Pull & Redeploy')).toBeInTheDocument()
  })
})
