/**
 * StackDetail was at 0/32 statements (agent-os-c1gu), and it is the one of that
 * bead's five modules with a standing obligation: it is stubbed at
 * pages/__tests__/StackPage.test.tsx, and agent-os-m1mu's criterion 5 says a
 * component may be stubbed in a page test OR tested directly, never neither.
 * This file is the "tested directly" half, so that stub is now legitimate.
 *
 * Its heavy children ARE stubbed here, which criterion 3 permits only because
 * every one of them has its own direct test: ContainerList, ComposeEditor,
 * EnvEditor, ComposeEnvSplit, LogViewer, Terminal, MetricsPanel,
 * StackUpdatesTab, BackupsTab, GitStatus, GitHistory, AutoUpdateToggle and
 * BackupToggle.
 *
 * What is left unstubbed is what this file is about: the tab wiring, the
 * Overview detail grid (Stack card + service deep links), and the ?container
 * pass-through. The lifecycle action bar moved to StackPage in the phase-3
 * redesign — its coverage lives in StackPage.test.tsx now.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import type { Stack } from '@/types'

const mockGetPolicies = vi.fn()
vi.mock('@/lib/api', () => ({
  autoUpdateApi: { getPolicies: (...a: unknown[]) => mockGetPolicies(...a) },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// jsdom has no WebSocket; the Overview grid's live-metrics stream is not under
// test here (useMetricsBase has its own direct test).
vi.mock('@/hooks/useMetricsBase', () => ({
  useMetricsBase: () => ({
    containers: [],
    baseAggregates: { totalCpuPercent: 0, totalMemUsage: 0, totalMemLimit: 0, totalMemPercent: 0 },
    latestMetrics: {},
    isConnected: false,
    ws: {},
  }),
}))

// Every stub below names a component that has its own direct test (see the file
// comment). Each renders a testid so the tab wiring can be asserted on.
// ContainerList's stub additionally exposes its deep-link callbacks so the
// Overview → Logs/Terminal wiring can be exercised.
vi.mock('../ContainerList', () => ({
  ContainerList: (props: {
    onShowLogs?: (name: string) => void
    onOpenShell?: (id: string) => void
  }) => (
    <div data-testid="container-list">
      <button onClick={() => props.onShowLogs?.('web')}>row-logs</button>
      <button onClick={() => props.onOpenShell?.('c1')}>row-shell</button>
    </div>
  ),
}))
vi.mock('../EnvEditor', () => ({ EnvEditor: () => <div data-testid="env-editor" /> }))
vi.mock('../ComposeEnvSplit', () => ({ ComposeEnvSplit: () => <div data-testid="compose-env-split" /> }))
vi.mock('../Terminal', () => ({
  TerminalComponent: ({ initialContainer }: { initialContainer?: string }) => (
    <div data-testid="terminal" data-initial-container={initialContainer ?? ''} />
  ),
}))
vi.mock('../LogViewer', () => ({
  LogViewer: ({ initialContainer }: { initialContainer?: string }) => (
    <div data-testid="log-viewer" data-initial-container={initialContainer ?? ''} />
  ),
}))
vi.mock('../StackUpdatesTab', () => ({ StackUpdatesTab: () => <div data-testid="updates-tab" /> }))
vi.mock('../BackupsTab', () => ({ BackupsTab: () => <div data-testid="backups-tab" /> }))
vi.mock('../../git/GitHistory', () => ({ GitHistory: () => <div data-testid="git-history" /> }))
vi.mock('@/components/dashboard/AutoUpdateToggle', () => ({
  AutoUpdateToggle: (props: { globalDisabled: boolean }) => (
    <div data-testid="auto-update-toggle" data-global-disabled={String(props.globalDisabled)} />
  ),
}))
vi.mock('@/components/dashboard/BackupToggle', () => ({ BackupToggle: () => <div data-testid="backup-toggle" /> }))
// These two are lazy()-loaded via `import(...).then(m => ({default: m.X}))`, so
// the mock has to provide the NAMED export — a `default` alone leaves the lazy
// wrapper resolving to undefined and React renders nothing.
vi.mock('../ComposeEditor', () => ({ ComposeEditor: () => <div data-testid="compose-editor" /> }))
vi.mock('../MetricsPanel', () => ({ MetricsPanel: () => <div data-testid="metrics-panel" /> }))

import { StackDetail } from '../StackDetail'

function stack(overrides: Partial<Stack> = {}): Stack {
  return {
    id: 'stack-1',
    projectName: 'my-stack',
    directory: '/srv/stacks/my-stack',
    composeFile: 'docker-compose.yaml',
    status: 'running',
    isGitRepo: false,
    gitDirty: false,
    gitAhead: 0,
    gitBehind: 0,
    containers: [],
    ...overrides,
  } as Stack
}

function renderDetail(
  props: { stack?: Stack; activeTab?: string; onTabChange?: (t: string) => void } = {},
  options: { route?: string } = {},
) {
  const onTabChange = props.onTabChange ?? vi.fn()
  const result = renderWithProviders(
    <StackDetail
      stack={props.stack ?? stack()}
      activeTab={props.activeTab ?? 'overview'}
      onTabChange={onTabChange}
    />,
    options,
  )
  return { ...result, onTabChange }
}

/**
 * The Overview grid renders several KvRows and more than one of them can show
 * an em dash at the same time (Env file does it for the default fixture), so a
 * bare screen.getByText('—') is ambiguous and a bare queryByText('—') asserts
 * about whichever row happens to match. This returns the Branch row itself —
 * KvRow puts the label and the value in two spans under one div — so the git
 * assertions are scoped to the row they are actually about.
 */
async function branchRow(): Promise<HTMLElement> {
  const label = await screen.findByText('Branch')
  const row = label.parentElement
  if (!row) throw new Error('the Branch KvRow label has no parent element')
  return row as HTMLElement
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetPolicies.mockResolvedValue({ policies: [], globalEnabled: true })
})

describe('StackDetail — tabs', () => {
  it('offers every tab', async () => {
    renderDetail()

    for (const label of [
      'Overview', 'Editor', 'Logs', 'Terminal', 'Metrics', 'Activity',
    ]) {
      expect(await screen.findByRole('tab', { name: label })).toBeInTheDocument()
    }
  })

  it('reports a tab change to its parent rather than owning the state', async () => {
    const user = userEvent.setup()
    const { onTabChange } = renderDetail()

    await user.click(await screen.findByRole('tab', { name: 'Logs' }))

    // The active tab is a prop, so the page owns it (and puts it in the URL).
    expect(onTabChange).toHaveBeenCalledWith('logs')
  })

  it.each([
    ['editor', 'compose-env-split'],
    ['logs', 'log-viewer'],
    ['terminal', 'terminal'],
    // Activity defaults to its History section.
    ['activity', 'git-history'],
  ])('renders the %s tab content when it is active', async (tab, testid) => {
    renderDetail({ activeTab: tab })
    expect(await screen.findByTestId(testid)).toBeInTheDocument()
  })

  it.each([
    ['metrics', 'metrics-panel'],
  ])('resolves the lazily-loaded %s tab behind Suspense', async (tab, testid) => {
    renderDetail({ activeTab: tab })
    // Lazy imports mean this is only present after the chunk resolves.
    expect(await screen.findByTestId(testid)).toBeInTheDocument()
  })

  it('switches Activity sections between History, Updates and Backups', async () => {
    const user = userEvent.setup()
    renderDetail({ activeTab: 'activity' })

    expect(await screen.findByTestId('git-history')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'Updates' }))
    expect(await screen.findByTestId('updates-tab')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'Backups' }))
    expect(await screen.findByTestId('backups-tab')).toBeInTheDocument()
  })

  it('mounts only the active tab, so no inactive tab pays for its children', async () => {
    renderDetail({ activeTab: 'overview' })

    await screen.findByTestId('container-list')
    expect(screen.queryByTestId('log-viewer')).not.toBeInTheDocument()
    expect(screen.queryByTestId('terminal')).not.toBeInTheDocument()
    expect(screen.queryByTestId('metrics-panel')).not.toBeInTheDocument()
  })

  // Git status moved out of StackDetail into the StackPage header chip
  // (see GitStatus.test.tsx), so StackDetail no longer renders it.
  // The lifecycle action bar (Start/Stop/Restart/Pull) likewise moved to the
  // StackPage header — see StackPage.test.tsx.
})

describe('StackDetail — Overview detail grid', () => {
  it('shows the Stack card facts from the stack payload', async () => {
    renderDetail({
      stack: stack({
        composeFile: 'docker-compose.yaml',
        envFile: '.env',
        containers: [
          { id: 'c1', name: 'web', image: 'nginx:1', state: 'running', status: 'Up', ports: [] },
        ] as Stack['containers'],
      }),
    })

    expect(await screen.findByText('Compose file')).toBeInTheDocument()
    expect(screen.getByText('docker-compose.yaml')).toBeInTheDocument()
    expect(screen.getByText('Env file')).toBeInTheDocument()
    expect(screen.getByText('.env')).toBeInTheDocument()
    // "Services" appears both as the left panel's header and as the kv row.
    expect(screen.getAllByText('Services').length).toBeGreaterThanOrEqual(2)
  })

  it('shows the git branch row only for git-backed stacks', async () => {
    renderDetail({ stack: stack({ isGitRepo: true, gitBranch: 'main' }) })

    expect(await screen.findByText('Branch')).toBeInTheDocument()
    expect(screen.getByText('main')).toBeInTheDocument()
  })

  /**
   * The Branch row is the other end of agent-os-jieh. The scanner used to send
   * an empty gitBranch for a detached HEAD, an unreadable HEAD, an unstat-able
   * .git and a worktree checkout alike, so all four rendered as the same em
   * dash and an operator could not tell a deliberate detached checkout from a
   * scan that failed. resolveGitState (backend/internal/services/scanner.go)
   * now sends a different string for each, and this row has to carry them
   * through unaltered.
   *
   * Whole-string assertions, for the same reason as the DirectoriesTab arm:
   * 'detached@abc1234' must not be satisfied by a branch named 'detached-x'.
   */
  it('renders the scanner’s detached and read-fault strings distinctly', async () => {
    for (const branch of ['detached@abc1234', 'unknown (read failed)', 'feature/login']) {
      const { unmount } = renderDetail({ stack: stack({ isGitRepo: true, gitBranch: branch }) })

      const row = await branchRow()
      expect(within(row).getByText(branch)).toBeInTheDocument()
      // Scoped to the Branch row on purpose: the Env file row above it renders
      // its own em dash for this fixture, so an unscoped queryByText('—') here
      // reports the WRONG row's content and passes for the wrong reason.
      expect(within(row).queryByText('—')).not.toBeInTheDocument()

      unmount()
    }
  })

  /**
   * The em dash is still reachable, and still correct, for a row an older
   * Capstan wrote and no rescan has replaced yet — and for a payload that omits
   * gitBranch entirely, which the `gitBranch?: string` declaration at
   * types/index.ts:104 permits. Both arms, because they take different routes
   * to the same `||`.
   */
  it('still falls back to the em dash for a stale or absent branch', async () => {
    const { unmount } = renderDetail({ stack: stack({ isGitRepo: true, gitBranch: '' }) })
    expect(within(await branchRow()).getByText('—')).toBeInTheDocument()
    unmount()

    renderDetail({ stack: stack({ isGitRepo: true }) })
    expect(within(await branchRow()).getByText('—')).toBeInTheDocument()
  })

  it('deep-links a service row to the Logs tab with the container preselected', async () => {
    const user = userEvent.setup()
    const { onTabChange } = renderDetail()

    await user.click(await screen.findByText('row-logs'))

    expect(onTabChange).toHaveBeenCalledWith('logs?container=web')
  })

  it('deep-links a service row to the Terminal tab with the container preselected', async () => {
    const user = userEvent.setup()
    const { onTabChange } = renderDetail()

    await user.click(await screen.findByText('row-shell'))

    expect(onTabChange).toHaveBeenCalledWith('terminal?container=c1')
  })
})

describe('StackDetail — ?container pass-through', () => {
  it('feeds ?container= to the log viewer as its initial selection', async () => {
    renderDetail({ activeTab: 'logs' }, { route: '/stacks/stack-1/logs?container=web' })

    const viewer = await screen.findByTestId('log-viewer')
    expect(viewer).toHaveAttribute('data-initial-container', 'web')
  })

  it('feeds ?container= to the terminal as its initial selection', async () => {
    renderDetail({ activeTab: 'terminal' }, { route: '/stacks/stack-1/terminal?container=c1' })

    const terminal = await screen.findByTestId('terminal')
    expect(terminal).toHaveAttribute('data-initial-container', 'c1')
  })
})

describe('StackDetail — auto-update wiring', () => {
  it('tells the toggle when auto-update is globally off', async () => {
    mockGetPolicies.mockResolvedValue({ policies: [], globalEnabled: false })
    renderDetail()

    const toggle = await screen.findByTestId('auto-update-toggle')
    await waitFor(() => expect(toggle).toHaveAttribute('data-global-disabled', 'true'))
  })

  it('treats auto-update as available when the global switch is on', async () => {
    mockGetPolicies.mockResolvedValue({ policies: [], globalEnabled: true })
    renderDetail()

    const toggle = await screen.findByTestId('auto-update-toggle')
    await waitFor(() => expect(toggle).toHaveAttribute('data-global-disabled', 'false'))
  })

  it('notes when individual containers carry their own policies', async () => {
    mockGetPolicies.mockResolvedValue({
      policies: [{ id: 'p1', targetType: 'container', targetId: 'c1', enabled: true }],
      globalEnabled: true,
    })
    renderDetail({
      stack: stack({ containers: [{ id: 'c1', name: 'web' }] as Stack['containers'] }),
    })

    // The stack-level toggle is overridden per container, which needs saying or
    // the toggle looks broken.
    expect(await screen.findByTestId('auto-update-toggle')).toBeInTheDocument()
    await waitFor(() => {
      expect(document.querySelector('svg.lucide-info')).not.toBeNull()
    })
  })

  it('renders the backup toggle in the Stack card', async () => {
    renderDetail()

    expect(await screen.findByTestId('backup-toggle')).toBeInTheDocument()
  })
})
