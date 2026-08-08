import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { StacksTab } from '../StacksTab'
import type { Stack } from '@/types'

/**
 * DashboardPage.test.tsx:75 replaces this tab with an empty div (0/32
 * statements, agent-os-m1mu). It takes everything as props, so it needs only a
 * Router. Nothing here is mocked except useNavigate.
 */

const mockNavigate = vi.fn()
vi.mock('react-router', async () => {
  const actual = await vi.importActual<typeof import('react-router')>('react-router')
  return { ...actual, useNavigate: () => mockNavigate }
})

const stack = (over: Partial<Stack> = {}): Stack => ({
  id: 's1',
  directory: '/srv/stacks/web',
  composeFile: 'compose.yml',
  projectName: 'web',
  status: 'running',
  isGitRepo: false,
  gitDirty: false,
  gitAhead: 0,
  gitBehind: 0,
  ...over,
})

const handlers = () => ({
  onSortChange: vi.fn(),
  onFilterChange: vi.fn(),
  onNavigateToDirectories: vi.fn(),
  onCreateStack: vi.fn(),
  onStart: vi.fn(),
  onStop: vi.fn(),
  onRestart: vi.fn(),
  onDelete: vi.fn(),
})

function renderTab(over: Partial<React.ComponentProps<typeof StacksTab>> = {}) {
  const h = handlers()
  const stacks = over.stacks ?? [stack()]
  const props = {
    stacks,
    filteredStacks: over.filteredStacks ?? stacks,
    configuredDirs: ['/srv/stacks'],
    sortBy: 'name' as const,
    statusFilter: 'all' as const,
    deletingStackId: null,
    startPending: false,
    stopPending: false,
    restartPending: false,
    deletePending: false,
    isAnimating: () => false,
    ...h,
    ...over,
  }
  return { ...render(<MemoryRouter><StacksTab {...props} /></MemoryRouter>), props }
}

beforeEach(() => vi.clearAllMocks())

describe('StacksTab — the table', () => {
  it('renders a row per stack with its compose file and status', () => {
    renderTab({ stacks: [stack({ projectName: 'web', composeFile: 'docker-compose.yml' })] })

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('docker-compose.yml')).toBeInTheDocument()
  })

  it('shows the container count, and a dash when there are none', () => {
    renderTab({
      stacks: [
        stack({ id: 's1', projectName: 'web', containers: [{}, {}, {}] as Stack['containers'] }),
        stack({ id: 's2', projectName: 'api', directory: '/srv/stacks/api' }),
      ],
    })

    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('-')).toBeInTheDocument()
  })

  it('navigates to a stack when its row is clicked', () => {
    renderTab({ stacks: [stack({ id: 'stack-42' })] })

    fireEvent.click(screen.getByText('web'))

    expect(mockNavigate).toHaveBeenCalledWith('/stacks/stack-42')
  })

  it('navigates on Enter and on Space, so the row is keyboard-reachable', () => {
    renderTab({ stacks: [stack({ id: 'stack-42' })] })

    const row = screen.getByRole('link')
    fireEvent.keyDown(row, { key: 'Enter' })
    fireEvent.keyDown(row, { key: ' ' })

    expect(mockNavigate).toHaveBeenCalledTimes(2)
    expect(mockNavigate).toHaveBeenLastCalledWith('/stacks/stack-42')
  })

  it('ignores other keys', () => {
    renderTab()

    fireEvent.keyDown(screen.getByRole('link'), { key: 'a' })

    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('sends the compose-file button to the directories tab without navigating to the stack', () => {
    const { props } = renderTab()

    fireEvent.click(screen.getByRole('button', { name: /compose\.yml/ }))

    expect(props.onNavigateToDirectories).toHaveBeenCalledTimes(1)
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

describe('StacksTab — grouping', () => {
  it('groups stacks under a directory header once there is more than one', () => {
    renderTab({
      stacks: [
        stack({ id: 's1', projectName: 'web', directory: '/srv/stacks/apps' }),
        stack({ id: 's2', projectName: 'api', directory: '/srv/stacks/apps' }),
      ],
    })

    expect(screen.getByText('2 stacks')).toBeInTheDocument()
    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
  })

  it('uses singular wording for a single-stack group', () => {
    renderTab({
      stacks: [
        stack({ id: 's1', projectName: 'web', directory: '/srv/stacks/a' }),
        stack({ id: 's2', projectName: 'api', directory: '/srv/stacks/b' }),
      ],
    })

    expect(screen.getAllByText('1 stack').length).toBeGreaterThan(0)
  })

  it('renders a flat list, with no group header, for a single stack', () => {
    renderTab({ stacks: [stack()] })

    expect(screen.queryByText(/^\d+ stacks?$/)).not.toBeInTheDocument()
  })
})

describe('StacksTab — filtering', () => {
  const TWO = [
    stack({ id: 's1', projectName: 'web' }),
    stack({ id: 's2', projectName: 'api', directory: '/srv/stacks/api' }),
  ]

  it('filters by project name', () => {
    renderTab({ stacks: TWO })

    fireEvent.change(screen.getByPlaceholderText('Filter stacks…'), { target: { value: 'web' } })

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.queryByText('api')).not.toBeInTheDocument()
  })

  it('filters by status too', () => {
    renderTab({
      stacks: [
        stack({ id: 's1', projectName: 'web', status: 'running' }),
        stack({ id: 's2', projectName: 'api', status: 'stopped', directory: '/srv/stacks/api' }),
      ],
    })

    fireEvent.change(screen.getByPlaceholderText('Filter stacks…'), {
      target: { value: 'stopped' },
    })

    expect(screen.getByText('api')).toBeInTheDocument()
    expect(screen.queryByText('web')).not.toBeInTheDocument()
  })

  it('switches the count display to "n of m stacks" while filtering', () => {
    renderTab({ stacks: TWO })

    fireEvent.change(screen.getByPlaceholderText('Filter stacks…'), { target: { value: 'web' } })

    expect(screen.getByText('1 of 2 stacks')).toBeInTheDocument()
  })

  it('quotes the query when the text filter matches nothing', () => {
    renderTab({ stacks: TWO })

    fireEvent.change(screen.getByPlaceholderText('Filter stacks…'), { target: { value: 'zzz' } })

    expect(screen.getByText('No stacks match "zzz"')).toBeInTheDocument()
    // Offering "create your first stack" here would be wrong — there are two.
    expect(screen.queryByRole('button', { name: /Create Your First Stack/ })).not.toBeInTheDocument()
  })

  it('forwards sort and status-filter changes to the parent', () => {
    const { props } = renderTab({ stacks: TWO })

    fireEvent.click(screen.getByRole('button', { name: 'Status' }))
    expect(props.onSortChange).toHaveBeenCalledWith('status')

    fireEvent.click(screen.getByRole('button', { name: 'Running' }))
    expect(props.onFilterChange).toHaveBeenCalledWith('running')
  })
})

describe('StacksTab — empty states', () => {
  it('invites the user to create their first stack when there are none', () => {
    const { props } = renderTab({ stacks: [], filteredStacks: [] })

    expect(screen.getByText('No stacks configured yet')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Create Your First Stack/ }))
    expect(props.onCreateStack).toHaveBeenCalledTimes(1)
  })

  it('names the active status filter when it is what emptied the list', () => {
    renderTab({ stacks: [stack()], filteredStacks: [], statusFilter: 'stopped' })

    expect(screen.getByText('No stopped stacks found')).toBeInTheDocument()
    // The create button belongs to the genuinely-empty case only.
    expect(screen.queryByRole('button', { name: /Create Your First Stack/ })).not.toBeInTheDocument()
  })
})

describe('StacksTab — row actions', () => {
  it('passes the stack through to the start handler', () => {
    const { props } = renderTab({ stacks: [stack({ id: 's1', status: 'stopped' })] })

    fireEvent.click(screen.getByRole('button', { name: /start/i }))

    expect(props.onStart).toHaveBeenCalledWith('s1', expect.anything())
  })

  it('does not navigate to the stack when a row action is used', () => {
    renderTab({ stacks: [stack({ id: 's1', status: 'stopped' })] })

    fireEvent.click(screen.getByRole('button', { name: /start/i }))

    expect(mockNavigate).not.toHaveBeenCalled()
  })
})
