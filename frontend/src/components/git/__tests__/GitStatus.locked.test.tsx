import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'

const mockUseGitStatus = vi.fn()
const mockUseGitPull = vi.fn(() => ({ mutate: vi.fn(), isPending: false }))

vi.mock('@/hooks/useGit', () => ({
  useGitStatus: (...args: unknown[]) => mockUseGitStatus(...args),
  useGitPull: () => mockUseGitPull(),
  // The commit actions in the popover (agent-os-k81p.3); nothing to commit here.
  useGitChanges: () => ({ data: undefined }),
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

// agent-os-hblm: while env values are locked the server refuses a pull, so
// both pull buttons are disabled with the unlock notice beside them.
describe('GitStatus while env values are locked', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseGitPull.mockReturnValue({ mutate: vi.fn(), isPending: false })
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: gitData() })
    useAuthStore.setState({ authDisabled: false })
    useEnvUnlockStore.getState().lock()
  })

  it('disables Git Pull and Pull & Redeploy and offers an unlock', async () => {
    const user = userEvent.setup()
    renderWithProviders(<GitStatus stack={mockStack} />)
    await user.click(screen.getByRole('button', { name: /Git status/ }))
    expect(await screen.findByRole('button', { name: /Git Pull/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Pull & Redeploy/ })).toBeDisabled()
    expect(screen.getByText(/Pulling needs an unlock/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument()
  })

  it('unlocked, both pull buttons are enabled and no notice shows', async () => {
    useEnvUnlockStore.getState().unlock('t')
    const user = userEvent.setup()
    renderWithProviders(<GitStatus stack={mockStack} />)
    await user.click(screen.getByRole('button', { name: /Git status/ }))
    expect(await screen.findByRole('button', { name: /Git Pull/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /Pull & Redeploy/ })).toBeEnabled()
    expect(screen.queryByText(/Pulling needs an unlock/)).toBeNull()
  })
})
