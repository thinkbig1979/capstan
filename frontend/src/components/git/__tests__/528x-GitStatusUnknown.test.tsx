/**
 * agent-os-528x. A failed git probe used to render NOTHING: GitStatus folded
 * "still loading", "request failed" and "no data" into one `return null`, so a
 * probe fault took the branch, the commit and the Pull button away with no
 * reason given. Before agent-os-ufj7 the same fault read "clean" and left Pull
 * enabled, which was worse: a pull on a dirty tree is the dangerous operation.
 *
 * What these pin:
 *  - loading and error render differently;
 *  - an error renders "status unknown", never "clean" and never nothing, and
 *    shows the branch and commit when an earlier payload carried them;
 *  - Pull is present but NOT actionable in every unknown state;
 *  - the two routine 200 answers (isRepo false, hasCommits false) are not
 *    swept into the unknown state, and a readable repository still pulls.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

const mockUseGitStatus = vi.fn()
const mockMutate = vi.fn()
const mockUseGitPull = vi.fn(() => ({ mutate: mockMutate, isPending: false }))

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
    scan: vi.fn(),
  },
}))

import { GitStatus } from '../GitStatus'

// The stack's cached git fields name a branch and say clean. The unknown
// state must not borrow them: they come from the scanner, not from this probe.
const mockStack = {
  envFile: '',
  gitBranch: 'cached-branch',
  gitCommit: 'cached',
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

// lib/api.ts's interceptor shape for the 500 agent-os-ufj7 returns on a probe
// fault: the body spread flat with the HTTP status beside it.
const probeFault = {
  status: 500,
  code: 'INTERNAL_ERROR',
  error: 'failed to read status',
  message: 'failed to read status',
}

function repoData(overrides: Record<string, unknown> = {}) {
  return {
    isRepo: true,
    hasCommits: true,
    branch: 'feature',
    commit: 'abc123',
    commitShort: 'abc1234',
    commitMessage: 'test',
    commitAuthor: 'test',
    commitDate: '2024-01-01',
    dirty: false,
    dirtyCount: 0,
    ahead: 2,
    behind: 1,
    ...overrides,
  }
}

function hookState(state: { isLoading?: boolean; error?: unknown; data?: unknown }) {
  return { isLoading: false, error: null, data: undefined, refetch: vi.fn(), ...state }
}

async function openPopover(name: RegExp | string) {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name }))
  return user
}

/** Both pull buttons exist, are disabled, and clicking them does nothing. */
async function expectPullNotActionable(user: ReturnType<typeof userEvent.setup>) {
  const pull = await screen.findByRole('button', { name: /Git Pull/ })
  const redeploy = screen.getByRole('button', { name: /Pull & Redeploy/ })
  expect(pull).toBeDisabled()
  expect(redeploy).toBeDisabled()
  expect(screen.getByText(/Pull is disabled: the working tree's state could not be read/)).toBeInTheDocument()
  await user.click(pull)
  await user.click(redeploy)
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(screen.queryByText('Confirm Pull')).not.toBeInTheDocument()
  expect(mockMutate).not.toHaveBeenCalled()
}

describe('GitStatus unknown state (agent-os-528x)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: mockMutate, isPending: false })
  })

  it('renders loading and a failed request differently', () => {
    mockUseGitStatus.mockReturnValue(hookState({ isLoading: true }))
    const { container, unmount } = renderWithProviders(<GitStatus stack={mockStack} />)
    expect(container).toBeEmptyDOMElement()
    unmount()

    mockUseGitStatus.mockReturnValue(hookState({ error: probeFault }))
    const failed = renderWithProviders(<GitStatus stack={mockStack} />)
    expect(failed.container).not.toBeEmptyDOMElement()
    expect(screen.getByRole('button', { name: 'Git status: unknown' })).toBeInTheDocument()
  })

  it('a probe fault with no data says unknown, never clean, and borrows nothing from the cache', async () => {
    mockUseGitStatus.mockReturnValue(hookState({ error: probeFault }))
    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.getByText('git status unknown')).toBeInTheDocument()
    expect(screen.queryByText(/clean/)).not.toBeInTheDocument()
    expect(screen.queryByText('cached-branch')).not.toBeInTheDocument()

    const user = await openPopover('Git status: unknown')
    expect(await screen.findByText('Could not read the git status.')).toBeInTheDocument()
    await expectPullNotActionable(user)
  })

  it('offers Retry, which refetches', async () => {
    const state = hookState({ error: probeFault })
    mockUseGitStatus.mockReturnValue(state)
    renderWithProviders(<GitStatus stack={mockStack} />)

    const user = await openPopover('Git status: unknown')
    await user.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(state.refetch).toHaveBeenCalledTimes(1)
  })

  // The `!error` term of the Pull gate. react-query keeps the last good payload
  // when a refetch fails, so without that term a stale "clean" enables Pull.
  it('a failed refetch keeps the known fields, drops the status ones, and disables Pull', async () => {
    mockUseGitStatus.mockReturnValue(hookState({ error: probeFault, data: repoData() }))
    renderWithProviders(<GitStatus stack={mockStack} />)

    const chip = screen.getByRole('button', { name: 'Git status: feature, status unknown' })
    expect(chip).toHaveTextContent('feature')
    expect(chip).toHaveTextContent('status unknown')
    expect(chip).not.toHaveTextContent('clean')
    // ahead 2 / behind 1 were in the stale payload; neither is claimed now.
    expect(chip).not.toHaveTextContent('2')

    const user = await openPopover('Git status: feature, status unknown')
    expect(await screen.findByText(/Could not refresh the git status/)).toBeInTheDocument()
    expect(screen.getByText('abc1234')).toBeInTheDocument()
    expect(screen.queryByText('2 ahead')).not.toBeInTheDocument()
    expect(screen.queryByText('1 behind')).not.toBeInTheDocument()
    await expectPullNotActionable(user)
  })

  it('a failed refetch over a DIRTY payload does not show the stale dirty count either', async () => {
    mockUseGitStatus.mockReturnValue(
      hookState({ error: probeFault, data: repoData({ dirty: true, dirtyCount: 3 }) })
    )
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.queryByText(/3 dirty/)).not.toBeInTheDocument()
    expect(screen.getByText(/status unknown/)).toBeInTheDocument()
  })

  it('CONTROL: a readable repository still pulls', async () => {
    mockUseGitStatus.mockReturnValue(hookState({ data: repoData() }))
    renderWithProviders(<GitStatus stack={mockStack} />)

    expect(screen.queryByText(/status unknown/)).not.toBeInTheDocument()
    const user = await openPopover(/Git status: feature, clean/)
    const pull = await screen.findByRole('button', { name: /Git Pull/ })
    expect(pull).toBeEnabled()
    expect(screen.queryByText(/Pull is disabled/)).not.toBeInTheDocument()
    await user.click(pull)
    expect(await screen.findByText('Confirm Pull')).toBeInTheDocument()
  })

  it('CONTROL: Pull is disabled while a pull is already running', async () => {
    mockUseGitPull.mockReturnValue({ mutate: mockMutate, isPending: true })
    mockUseGitStatus.mockReturnValue(hookState({ data: repoData() }))
    renderWithProviders(<GitStatus stack={mockStack} />)
    await openPopover(/Git status: feature, clean/)
    expect(await screen.findByRole('button', { name: /Git Pull/ })).toBeDisabled()
  })

  it('CONTROL: isRepo false (agent-os-x40a) is not rendered as unknown', () => {
    mockUseGitStatus.mockReturnValue(hookState({ data: { isRepo: false } }))
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('not a git repository')).toBeInTheDocument()
    expect(screen.queryByText(/unknown/)).not.toBeInTheDocument()
  })

  it('CONTROL: hasCommits false (agent-os-4a4a) is not rendered as unknown', () => {
    mockUseGitStatus.mockReturnValue(hookState({ data: { isRepo: true, hasCommits: false } }))
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('no commits')).toBeInTheDocument()
    expect(screen.queryByText(/unknown/)).not.toBeInTheDocument()
  })
})
