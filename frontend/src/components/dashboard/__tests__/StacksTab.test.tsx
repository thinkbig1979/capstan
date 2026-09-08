import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { StacksTab } from '../StacksTab'
import type { AutoUpdatePolicy, Stack } from '@/types'

/**
 * DashboardPage.test.tsx:75 replaces this tab with an empty div (0/32
 * statements, agent-os-m1mu). It takes everything as props, so it needs only a
 * Router. Nothing here is mocked except useNavigate and the two toggles.
 */

const mockNavigate = vi.fn()
vi.mock('react-router', async () => {
  const actual = await vi.importActual<typeof import('react-router')>('react-router')
  return { ...actual, useNavigate: () => mockNavigate }
})

/**
 * Behaviour-bearing toggle stubs, deliberately NOT the inert `<div data-testid/>`
 * stubs UpdatesTab.test.tsx uses. Those carry no Switch, no aria-label and no
 * locked-variant testid, which makes the click/keyboard/locked criteria for this
 * table unprovable. These render the real `<Switch>` with the real components'
 * aria-labels and testids, call an injected spy, and honour the locked branch,
 * so the assertions below are about a contract the real components satisfy.
 */
const { autoUpdateChange, backupChange, backupState } = vi.hoisted(() => ({
  autoUpdateChange: vi.fn(),
  backupChange: vi.fn(),
  // The real BackupToggle self-fetches its engine status and takes no prop for
  // it, so "engine unavailable" is a module flag rather than a stub prop.
  backupState: { engineUnavailable: false },
}))

vi.mock('@/components/dashboard/AutoUpdateToggle', async () => {
  const { Switch } = await vi.importActual<typeof import('@/components/ui/switch')>(
    '@/components/ui/switch',
  )
  return {
    AutoUpdateToggle: ({
      targetType,
      targetId,
      enabled,
      globalDisabled,
    }: {
      targetType: string
      targetId: string
      enabled: boolean
      globalDisabled?: boolean
    }) =>
      globalDisabled ? (
        <Switch
          checked={false}
          disabled
          aria-label={`Auto-update ${targetType} ${targetId} (locked, global auto-update is off)`}
        />
      ) : (
        <Switch
          checked={enabled}
          onCheckedChange={(checked: boolean) => autoUpdateChange(targetId, checked)}
          aria-label={`Auto-update ${targetType} ${targetId}`}
        />
      ),
  }
})

vi.mock('@/components/dashboard/BackupToggle', async () => {
  const { Switch } = await vi.importActual<typeof import('@/components/ui/switch')>(
    '@/components/ui/switch',
  )
  return {
    BackupToggle: ({ stackId, showLastRunStatus }: { stackId: string; showLastRunStatus?: boolean }) =>
      backupState.engineUnavailable ? (
        <div data-testid={`backup-toggle-disabled-${stackId}`}>
          <Switch checked={false} disabled aria-label={`Backup stack ${stackId}`} />
        </div>
      ) : (
        <div data-testid={`backup-toggle-${stackId}`} data-last-run-status={String(showLastRunStatus)}>
          <Switch
            checked={false}
            onCheckedChange={(checked: boolean) => backupChange(stackId, checked)}
            aria-label={`Backup stack ${stackId}`}
            data-testid={`backup-switch-${stackId}`}
          />
        </div>
      ),
  }
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

const policy = (over: Partial<AutoUpdatePolicy> = {}): AutoUpdatePolicy => ({
  id: 'p1',
  targetType: 'stack',
  targetId: 's1',
  enabled: true,
  consecutiveFailures: 0,
  paused: false,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  ...over,
})

const handlers = () => ({
  onSortChange: vi.fn(),
  onFilterChange: vi.fn(),
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
    autoUpdatePolicies: [],
    globalAutoUpdateEnabled: true,
    ...h,
    ...over,
  }
  return { ...render(<MemoryRouter><StacksTab {...props} /></MemoryRouter>), props }
}

/** Two stacks in one directory, so buildDirectoryTree takes the grouped branch. */
const GROUPED = [
  stack({ id: 's1', projectName: 'web', directory: '/srv/stacks/apps' }),
  stack({ id: 's2', projectName: 'api', directory: '/srv/stacks/apps' }),
]

beforeEach(() => {
  vi.clearAllMocks()
  backupState.engineUnavailable = false
})

describe('StacksTab — the table', () => {
  it('renders exactly the six columns, in order', () => {
    renderTab()

    expect(screen.getAllByRole('columnheader').map((th) => th.textContent)).toEqual([
      'Name',
      'Status',
      'Containers',
      'Auto update',
      'Auto backup',
      'Actions',
    ])
  })

  it('renders a row per stack with its name, and no compose filename', () => {
    renderTab({ stacks: [stack({ projectName: 'web', composeFile: 'docker-compose.yml' })] })

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.queryByText('docker-compose.yml')).not.toBeInTheDocument()
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
})

describe('StacksTab — the git indicator', () => {
  it('marks a git-backed stack in the flat branch', () => {
    renderTab({ stacks: [stack({ id: 's1', isGitRepo: true })] })

    // No group header: this is the flat branch.
    expect(screen.queryByText(/^\d+ stacks?$/)).not.toBeInTheDocument()
    expect(screen.getByTestId('git-repo-s1')).toBeInTheDocument()
  })

  it('marks each git-backed stack in the grouped branch, per stack and not per directory', () => {
    renderTab({
      stacks: [
        stack({ id: 's1', projectName: 'web', directory: '/srv/stacks/apps', isGitRepo: true }),
        stack({ id: 's2', projectName: 'api', directory: '/srv/stacks/apps', isGitRepo: false }),
      ],
    })

    // A group header is present: this is the grouped branch.
    expect(screen.getByText('2 stacks')).toBeInTheDocument()
    expect(screen.getByTestId('git-repo-s1')).toBeInTheDocument()
    // The directory's first stack is a repo and the second is not. A row
    // indicator keyed on the directory (node.stacks[0]) would mark both.
    expect(screen.queryByTestId('git-repo-s2')).not.toBeInTheDocument()
  })
})

describe('StacksTab — grouping', () => {
  it('groups stacks under a directory header once there is more than one', () => {
    renderTab({ stacks: GROUPED })

    expect(screen.getByText('2 stacks')).toBeInTheDocument()
    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
  })

  it('spans the group header across every column, so the two cannot drift', () => {
    renderTab({ stacks: GROUPED })

    const columnCount = screen.getAllByRole('columnheader').length
    const groupCell = screen.getByText('2 stacks').closest('td')

    expect(groupCell).not.toBeNull()
    expect(groupCell?.getAttribute('colspan')).toBe(String(columnCount))
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

describe('StacksTab — the toggle columns', () => {
  it('reflects the matching stack policy, ignoring a container policy with the same id', () => {
    renderTab({
      stacks: [stack({ id: 's1' })],
      autoUpdatePolicies: [
        policy({ id: 'p-container', targetType: 'container', targetId: 's1', enabled: false }),
        policy({ id: 'p-stack', targetType: 'stack', targetId: 's1', enabled: true }),
      ],
    })

    expect(screen.getByRole('switch', { name: 'Auto-update stack s1' })).toBeChecked()
  })

  it('suppresses the install-wide last-run icon, which would repeat on every row', () => {
    renderTab({ stacks: GROUPED })

    expect(screen.getByTestId('backup-toggle-s1')).toHaveAttribute('data-last-run-status', 'false')
    expect(screen.getByTestId('backup-toggle-s2')).toHaveAttribute('data-last-run-status', 'false')
  })

  it('toggles auto-update without navigating, while the name cell still navigates', () => {
    renderTab({ stacks: [stack({ id: 's1' })] })

    fireEvent.click(screen.getByRole('switch', { name: 'Auto-update stack s1' }))

    expect(autoUpdateChange).toHaveBeenCalledWith('s1', true)
    expect(mockNavigate).not.toHaveBeenCalled()

    // Same test, same instrument, the case that MUST navigate — otherwise
    // "did not navigate" is equally consistent with a row that never navigates.
    fireEvent.click(screen.getByText('web'))

    expect(mockNavigate).toHaveBeenCalledWith('/stacks/s1')
  })

  it('toggles backup without navigating, while the name cell still navigates', () => {
    renderTab({ stacks: [stack({ id: 's1' })] })

    fireEvent.click(screen.getByTestId('backup-switch-s1'))

    expect(backupChange).toHaveBeenCalledWith('s1', true)
    expect(mockNavigate).not.toHaveBeenCalled()

    fireEvent.click(screen.getByText('web'))

    expect(mockNavigate).toHaveBeenCalledWith('/stacks/s1')
  })

  it('does not navigate when Space is pressed on either switch, though the row still does', () => {
    renderTab({ stacks: [stack({ id: 's1' })] })

    fireEvent.keyDown(screen.getByRole('switch', { name: 'Auto-update stack s1' }), { key: ' ' })
    fireEvent.keyDown(screen.getByTestId('backup-switch-s1'), { key: ' ' })

    expect(mockNavigate).not.toHaveBeenCalled()

    fireEvent.keyDown(screen.getByRole('link'), { key: ' ' })

    expect(mockNavigate).toHaveBeenCalledWith('/stacks/s1')
  })

  it('locks every Auto update cell when the global master switch is off', () => {
    renderTab({ stacks: GROUPED, globalAutoUpdateEnabled: false })

    for (const id of ['s1', 's2']) {
      const locked = screen.getByRole('switch', {
        name: `Auto-update stack ${id} (locked, global auto-update is off)`,
      })
      expect(locked).toBeDisabled()
    }
    expect(screen.queryByRole('switch', { name: 'Auto-update stack s1' })).not.toBeInTheDocument()
  })

  it('leaves the Auto update cells unlocked when the master switch is on', () => {
    renderTab({ stacks: GROUPED, globalAutoUpdateEnabled: true })

    for (const id of ['s1', 's2']) {
      expect(screen.getByRole('switch', { name: `Auto-update stack ${id}` })).toBeEnabled()
    }
    expect(
      screen.queryByRole('switch', { name: /Auto-update stack s1 \(locked/ }),
    ).not.toBeInTheDocument()
  })

  it('locks every Auto backup cell when the backup engine is unavailable', () => {
    backupState.engineUnavailable = true

    renderTab({ stacks: GROUPED })

    expect(screen.getByTestId('backup-toggle-disabled-s1')).toBeInTheDocument()
    expect(screen.getByTestId('backup-toggle-disabled-s2')).toBeInTheDocument()
    // backup-switch-<id> exists only in the enabled branch.
    expect(screen.queryByTestId('backup-switch-s1')).not.toBeInTheDocument()
  })

  it('leaves the Auto backup cells usable when the engine is available', () => {
    renderTab({ stacks: GROUPED })

    expect(screen.getByTestId('backup-switch-s1')).toBeEnabled()
    expect(screen.queryByTestId('backup-toggle-disabled-s1')).not.toBeInTheDocument()
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
