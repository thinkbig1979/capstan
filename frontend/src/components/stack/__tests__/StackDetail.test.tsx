/**
 * StackDetail was at 0/32 statements (agent-os-c1gu), and it is the one of that
 * bead's five modules with a standing obligation: it is stubbed at
 * pages/__tests__/StackPage.test.tsx:35, and agent-os-m1mu's criterion 5 says a
 * component may be stubbed in a page test OR tested directly, never neither.
 * This file is the "tested directly" half, so that stub is now legitimate.
 *
 * Its heavy children ARE stubbed here, which criterion 3 permits only because
 * every one of them has its own direct test: ContainerList, ComposeEditor,
 * EnvEditor, ComposeEnvSplit, LogViewer, Terminal, MetricsPanel,
 * StackUpdatesTab, BackupsTab, OperationProgress, GitStatus, GitHistory,
 * AutoUpdateToggle and BackupToggle. MetricsPanel's is MetricsPanel.test.tsx,
 * added in the same bead — which is why StackDetail came last.
 *
 * What is left unstubbed is what this file is about: the tab wiring, the action
 * bar's enable/disable rules, and the post-operation refresh.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { toast } from 'sonner'
import type { Stack } from '@/types'

const execute = vi.fn()
const reset = vi.fn()
// Mutated per test to drive the running/success branches.
let operationState = { status: 'idle', action: '', error: null as string | null, lines: [] as string[] }

vi.mock('@/hooks/useStreamingOperation', () => ({
  useStreamingOperation: () => ({ ...operationState, execute, cancel: vi.fn(), reset }),
}))

const mockGetPolicies = vi.fn()
vi.mock('@/lib/api', () => ({
  autoUpdateApi: { getPolicies: (...a: unknown[]) => mockGetPolicies(...a) },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// Every stub below names a component that has its own direct test (see the file
// comment). Each renders a testid so the tab wiring can be asserted on.
vi.mock('../ContainerList', () => ({ ContainerList: () => <div data-testid="container-list" /> }))
vi.mock('../EnvEditor', () => ({ EnvEditor: () => <div data-testid="env-editor" /> }))
vi.mock('../ComposeEnvSplit', () => ({ ComposeEnvSplit: () => <div data-testid="compose-env-split" /> }))
vi.mock('../Terminal', () => ({ TerminalComponent: () => <div data-testid="terminal" /> }))
vi.mock('../LogViewer', () => ({ LogViewer: () => <div data-testid="log-viewer" /> }))
vi.mock('../StackUpdatesTab', () => ({ StackUpdatesTab: () => <div data-testid="updates-tab" /> }))
vi.mock('../BackupsTab', () => ({ BackupsTab: () => <div data-testid="backups-tab" /> }))
vi.mock('../OperationProgress', () => ({
  OperationProgress: ({ status, action }: { status: string; action: string }) => (
    <div data-testid="operation-progress" data-status={status} data-action={action} />
  ),
}))
vi.mock('../../git/GitStatus', () => ({ GitStatus: () => <div data-testid="git-status" /> }))
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
    name: 'my-stack',
    path: '/srv/stacks/my-stack',
    status: 'running',
    containers: [],
    ...overrides,
  } as Stack
}

function renderDetail(props: { stack?: Stack; activeTab?: string; onTabChange?: (t: string) => void } = {}) {
  const onTabChange = props.onTabChange ?? vi.fn()
  const result = renderWithProviders(
    <StackDetail
      stack={props.stack ?? stack()}
      activeTab={props.activeTab ?? 'overview'}
      onTabChange={onTabChange}
    />,
  )
  return { ...result, onTabChange }
}

beforeEach(() => {
  vi.clearAllMocks()
  operationState = { status: 'idle', action: '', error: null, lines: [] }
  mockGetPolicies.mockResolvedValue({ policies: [], globalEnabled: true })
})

describe('StackDetail — tabs', () => {
  it('offers every tab', async () => {
    renderDetail()

    for (const label of [
      'Overview', 'History', 'Compose', 'Environment', 'Compose + Env',
      'Logs', 'Terminal', 'Metrics', 'Updates', 'Backups',
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
    ['history', 'git-history'],
    ['environment', 'env-editor'],
    ['split', 'compose-env-split'],
    ['logs', 'log-viewer'],
    ['terminal', 'terminal'],
    ['updates', 'updates-tab'],
    ['backups', 'backups-tab'],
  ])('renders the %s tab content when it is active', async (tab, testid) => {
    renderDetail({ activeTab: tab })
    expect(await screen.findByTestId(testid)).toBeInTheDocument()
  })

  it.each([
    ['compose', 'compose-editor'],
    ['metrics', 'metrics-panel'],
  ])('resolves the lazily-loaded %s tab behind Suspense', async (tab, testid) => {
    renderDetail({ activeTab: tab })
    // Lazy imports mean this is only present after the chunk resolves.
    expect(await screen.findByTestId(testid)).toBeInTheDocument()
  })

  it('mounts only the active tab, so no inactive tab pays for its children', async () => {
    renderDetail({ activeTab: 'overview' })

    await screen.findByTestId('container-list')
    expect(screen.queryByTestId('log-viewer')).not.toBeInTheDocument()
    expect(screen.queryByTestId('terminal')).not.toBeInTheDocument()
    expect(screen.queryByTestId('metrics-panel')).not.toBeInTheDocument()
  })

  it('shows git status above the tabs on every tab', async () => {
    renderDetail({ activeTab: 'logs' })
    expect(await screen.findByTestId('git-status')).toBeInTheDocument()
  })
})

describe('StackDetail — the action bar', () => {
  it('runs the matching operation for each button', async () => {
    const user = userEvent.setup()
    renderDetail({ stack: stack({ status: 'running' }) })

    await user.click(await screen.findByRole('button', { name: /Restart/ }))
    expect(execute).toHaveBeenCalledWith('stack-1', 'restart')

    await user.click(screen.getByRole('button', { name: /Pull Images/ }))
    expect(execute).toHaveBeenCalledWith('stack-1', 'pull')

    await user.click(screen.getByRole('button', { name: /Stop/ }))
    expect(execute).toHaveBeenCalledWith('stack-1', 'stop')
  })

  it('starts a stopped stack', async () => {
    const user = userEvent.setup()
    renderDetail({ stack: stack({ status: 'stopped' }) })

    await user.click(await screen.findByRole('button', { name: /Start/ }))
    expect(execute).toHaveBeenCalledWith('stack-1', 'start')
  })

  it('offers Start but not Stop on a stopped stack', async () => {
    renderDetail({ stack: stack({ status: 'stopped' }) })

    expect(await screen.findByRole('button', { name: /Start/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /Stop/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Restart/ })).toBeDisabled()
  })

  it('offers Stop but not Start on a running stack', async () => {
    renderDetail({ stack: stack({ status: 'running' }) })

    expect(await screen.findByRole('button', { name: /Stop/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /Start/ })).toBeDisabled()
  })

  it('lets a partially-running stack be started', async () => {
    renderDetail({ stack: stack({ status: 'partial' }) })

    // "partial" means some services are down, so Start is the useful action.
    expect(await screen.findByRole('button', { name: /Start/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /Stop/ })).toBeDisabled()
  })

  it('disables every action while one is already running', async () => {
    operationState = { status: 'running', action: 'start', error: null, lines: [] }
    renderDetail({ stack: stack({ status: 'stopped' }) })

    expect(await screen.findByRole('button', { name: /Starting\.\.\./ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Stop/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Restart/ })).toBeDisabled()
    // Pull has no status precondition of its own, but must not race a start.
    expect(screen.getByRole('button', { name: /Pull Images/ })).toBeDisabled()
  })

  it.each([
    ['start', 'Starting...'],
    ['stop', 'Stopping...'],
    ['restart', 'Restarting...'],
    ['pull', 'Pulling...'],
  ])('labels the in-flight %s button "%s"', async (action, label) => {
    operationState = { status: 'running', action, error: null, lines: [] }
    renderDetail()

    expect(await screen.findByText(label)).toBeInTheDocument()
  })

  it('passes the operation through to the progress panel', async () => {
    operationState = { status: 'running', action: 'pull', error: null, lines: ['pulling…'] }
    renderDetail()

    const progress = await screen.findByTestId('operation-progress')
    expect(progress).toHaveAttribute('data-status', 'running')
    expect(progress).toHaveAttribute('data-action', 'pull')
  })
})

describe('StackDetail — after an operation succeeds', () => {
  it('refreshes the stack and confirms the action by name', async () => {
    operationState = { status: 'success', action: 'restart', error: null, lines: [] }

    // The spy has to be in place before the mount, because the effect that
    // invalidates runs on it.
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    renderWithProviders(
      <StackDetail stack={stack()} activeTab="overview" onTabChange={vi.fn()} />,
      { queryClient },
    )

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('Restart completed')
    })
    // Stale data after a restart is the visible bug this guards: the status badge
    // would still read "stopped".
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['stack', 'stack-1'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['stacks'] })
  })

  it('says nothing while an operation is merely running', async () => {
    operationState = { status: 'running', action: 'start', error: null, lines: [] }
    renderDetail()

    await screen.findByTestId('operation-progress')
    expect(toast.success).not.toHaveBeenCalled()
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
})
