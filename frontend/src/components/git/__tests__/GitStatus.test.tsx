import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
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

function gitData(overrides: Record<string, unknown> = {}) {
  return {
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

  it('renders nothing when the directory is not a git repository', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: new Error('fail'), data: null })
    const { container } = renderWithProviders(<GitStatus stack={mockStack} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders branch name in the chip', () => {
    mockUseGitStatus.mockReturnValue({ isLoading: false, error: null, data: gitData() })
    renderWithProviders(<GitStatus stack={mockStack} />)
    expect(screen.getByText('main')).toBeInTheDocument()
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
