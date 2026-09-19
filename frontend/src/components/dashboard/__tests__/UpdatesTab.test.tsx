import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { UpdatesTab } from '../UpdatesTab'
import type { ContainerUpdateInfo, CachedUpdate, AutoUpdatePolicy } from '@/types'

// ─── Hook mocks ───────────────────────────────────────────────────────────────
// Mock the query/mutation and store hooks directly (mirrors BackupToggle.test.tsx)
// so the tests exercise UpdatesTab's own state machine, sort/filter, and policy
// resolution without depending on react-query internals.

const mockCheckUpdates = vi.fn()
const mockRefreshMutate = vi.fn()
const mockUpdateMutate = vi.fn()
let mockUpdateIsPending = false
const mockAutoUpdatePolicies = vi.fn()
const mockUpdateJobsHydrate = vi.fn()

vi.mock('@/hooks/useResources', () => ({
  useCheckUpdates: () => mockCheckUpdates(),
  useCheckUpdatesRefresh: () => ({ mutate: mockRefreshMutate }),
  useUpdateContainer: () => ({ mutate: mockUpdateMutate, isPending: mockUpdateIsPending }),
  useAutoUpdatePolicies: () => mockAutoUpdatePolicies(),
  useUpdateJobs: () => mockUpdateJobsHydrate(),
}))

let mockIsScanning = false

vi.mock('@/stores/updateScanStore', () => ({
  useUpdateScanStore: () => ({ isScanning: mockIsScanning }),
}))

let mockJobForContainer = (_containerId: string): unknown => undefined

vi.mock('@/stores/updateJobStore', () => ({
  useUpdateJobStore: (selector: (s: { jobForContainer: typeof mockJobForContainer }) => unknown) =>
    selector({ jobForContainer: mockJobForContainer }),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// Stub out child components with their own hook/WS dependencies so tests stay
// focused on UpdatesTab's own logic (mirrors DashboardPage stubbing UpdatesTab).
vi.mock('@/components/dashboard/AutoUpdateToggle', () => ({
  AutoUpdateToggle: (props: { targetType: string; targetId: string; enabled: boolean }) => (
    <div
      data-testid={`auto-update-toggle-${props.targetId}`}
      data-target-type={props.targetType}
      data-enabled={props.enabled}
    />
  ),
}))

vi.mock('@/components/dashboard/BackupToggle', () => ({
  BackupToggle: ({ stackId }: { stackId: string }) => (
    <div data-testid={`backup-toggle-${stackId}`} />
  ),
}))

vi.mock('@/components/updates/UpdateJobLog', () => ({
  UpdateJobLog: ({ job }: { job: { id: string } }) => (
    <div data-testid={`update-job-log-${job.id}`} />
  ),
}))

vi.mock('@/components/dashboard/UpdateLogTab', () => ({
  UpdateLogTab: () => <div data-testid="update-log-tab" />,
}))

import { toast } from 'sonner'

// ─── Fixture factories ────────────────────────────────────────────────────────

function makeContainer(overrides: Partial<ContainerUpdateInfo> = {}): ContainerUpdateInfo {
  return {
    containerId: 'c1',
    containerName: 'web',
    image: 'nginx',
    imageRef: 'nginx:latest',
    state: 'running',
    stackId: 'stack1',
    projectName: 'myproject',
    serviceName: 'web',
    isCompose: true,
    ...overrides,
  }
}

function makeCachedUpdate(overrides: Partial<CachedUpdate> = {}): CachedUpdate {
  return {
    id: 'u1',
    containerId: 'c2',
    containerName: 'api',
    image: 'api',
    imageRef: 'api:latest',
    state: 'stopped',
    isCompose: false,
    localDigest: 'sha256:1111111111111111111111',
    remoteDigest: 'sha256:2222222222222222222222',
    scannedAt: new Date().toISOString(),
    ...overrides,
  }
}

function makePolicy(overrides: Partial<AutoUpdatePolicy> = {}): AutoUpdatePolicy {
  return {
    id: 'p1',
    targetType: 'container',
    targetId: 'c1',
    enabled: true,
    consecutiveFailures: 0,
    paused: false,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    ...overrides,
  }
}

function setCheckUpdates(
  overrides: Partial<{ data: unknown; isLoading: boolean; isError: boolean; error: unknown }> = {},
) {
  mockCheckUpdates.mockReturnValue({
    data: undefined,
    isLoading: false,
    isError: false,
    // agent-os-rtn8: deliberately absent by default. Every pre-existing caller
    // of this helper sets isError WITHOUT an error object, and those callers are
    // the must-stay-green arm for the cause wiring below: a branch that read
    // classifyError(error) on isError alone would print the invented sentence
    // "An unexpected error occurred" for all of them.
    error: undefined,
    ...overrides,
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  mockIsScanning = false
  mockUpdateIsPending = false
  mockJobForContainer = () => undefined
  mockAutoUpdatePolicies.mockReturnValue({ data: { policies: [] } })
  setCheckUpdates()
})

// ─── State machine: scanning / loading / error / empty ────────────────────────

describe('UpdatesTab — Available Updates state machine', () => {
  it('shows the checking-for-updates card while a scan is in progress', () => {
    mockIsScanning = true
    setCheckUpdates()
    render(<UpdatesTab />)

    expect(screen.getByText('Checking for Updates')).toBeInTheDocument()
  })

  it('shows loading skeletons when the query is loading and no scan is running', () => {
    setCheckUpdates({ isLoading: true })
    render(<UpdatesTab />)

    expect(screen.queryByText('Checking for Updates')).not.toBeInTheDocument()
    expect(document.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0)
  })

  it('shows an error card when the query errors and there is no cached data', () => {
    setCheckUpdates({ isError: true })
    render(<UpdatesTab />)

    expect(screen.getByText('Failed to Check for Updates')).toBeInTheDocument()
  })

  it('retry button on the error card triggers a refresh', () => {
    setCheckUpdates({ isError: true })
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    expect(mockRefreshMutate).toHaveBeenCalled()
  })

  /**
   * agent-os-rtn8. useCheckUpdates calls resourcesApi.checkUpdates(FALSE), so
   * the only refusals that reach this card come from the non-refresh branch of
   * handlers/updates.go: three 500s carrying two distinct sentences, "Failed to
   * get cached updates" (the cache read) and "Failed to read the last scan time"
   * (the settings read, emitted from two places). "the registry is unreachable"
   * and "the database will not answer" are different operator problems and the
   * card said "An error occurred while checking for container image updates"
   * for both.
   *
   * NOT the 503 DOCKER_UNAVAILABLE path: that is emitted only under
   * refresh=true, which is useCheckUpdatesRefresh's route and already toasts
   * via resolveUpdateScanError. The backend's own docker_unavailable_test.go
   * route table lists `checkUpdatesRefresh` as "/api/resources/updates?refresh=true"
   * and does not list the bare route, which is the second arm on that.
   *
   * The causes reach us at all only because agent-os-mc4i stopped classifyError's
   * 5xx arm answering a bare status code; they keep that arm's deliberate
   * `${status}: ` prefix, since 503 vs 500 is itself diagnostic.
   */
  it.each([
    ['Failed to get cached updates', '500: Failed to get cached updates'],
    ['Failed to read the last scan time', '500: Failed to read the last scan time'],
  ])('names the cause the backend sent for a %s failure', (message, rendered) => {
    setCheckUpdates({ isError: true, error: { status: 500, code: 'INTERNAL_ERROR', message } })
    render(<UpdatesTab />)

    expect(screen.getByText('Failed to Check for Updates')).toBeInTheDocument()
    expect(screen.getByText(rendered)).toBeInTheDocument()
  })

  /**
   * The two-sided arm, and the reason the cause is gated on `error` rather than
   * on `isError`. classifyError(undefined) returns the INVENTED sentence "An
   * unexpected error occurred", so a branch keyed on isError alone would
   * manufacture a cause for a failure the backend never described -- the worse
   * defect, and the one this whole line of work exists to stop.
   *
   * The generic fallback line is asserted absent rather than the cause line,
   * because "no cause rendered" is what must hold.
   */
  it('invents no cause when the query errored without an error object', () => {
    setCheckUpdates({ isError: true })
    render(<UpdatesTab />)

    expect(screen.getByText('Failed to Check for Updates')).toBeInTheDocument()
    expect(screen.queryByText('An unexpected error occurred')).not.toBeInTheDocument()
    expect(screen.queryByText('An error occurred')).not.toBeInTheDocument()
  })

  it('does not show the error card when errored but serving cached data', () => {
    setCheckUpdates({ isError: true, data: { updates: [], fromCache: true } })
    render(<UpdatesTab />)

    expect(screen.queryByText('Failed to Check for Updates')).not.toBeInTheDocument()
  })

  it('shows the never-scanned empty state when no data, not loading, not erroring, not scanning', () => {
    setCheckUpdates()
    render(<UpdatesTab />)

    expect(screen.getByText('No Scan Data Available')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings')
  })

  it('"Check for Updates" button on the never-scanned state triggers a refresh', () => {
    setCheckUpdates()
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /check for updates/i }))

    expect(mockRefreshMutate).toHaveBeenCalled()
  })

  it('shows the all-up-to-date state when data has been scanned but has no updates', () => {
    setCheckUpdates({ data: { updates: [], fromCache: false, scannedAt: new Date().toISOString() } })
    render(<UpdatesTab />)

    expect(screen.getByText('All Containers Up to Date')).toBeInTheDocument()
  })

  it('"Check Again" button on the all-up-to-date state triggers a refresh', () => {
    setCheckUpdates({ data: { updates: [], fromCache: false, scannedAt: new Date().toISOString() } })
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /check again/i }))

    expect(mockRefreshMutate).toHaveBeenCalled()
  })
})

// ─── Table rendering, sort, filter ─────────────────────────────────────────────

describe('UpdatesTab — updates table', () => {
  it('renders one row per update and shows the available-count badge', () => {
    const containers = [makeContainer({ containerId: 'a', containerName: 'zeta' }), makeContainer({ containerId: 'b', containerName: 'alpha' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    expect(screen.getByText('zeta')).toBeInTheDocument()
    expect(screen.getByText('alpha')).toBeInTheDocument()
    // TabsList badge reflects updates.length
    expect(screen.getByText('2')).toBeInTheDocument()
  })

  it('sorts by name by default (localeCompare ascending)', () => {
    const containers = [makeContainer({ containerId: 'a', containerName: 'zeta' }), makeContainer({ containerId: 'b', containerName: 'alpha' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    const names = screen.getAllByText(/^(zeta|alpha)$/).map((el) => el.textContent)
    expect(names).toEqual(['alpha', 'zeta'])
  })

  it('re-sorts by image when the Image sort option is chosen', () => {
    const containers = [
      makeContainer({ containerId: 'a', containerName: 'zeta', imageRef: 'zzz:latest' }),
      makeContainer({ containerId: 'b', containerName: 'alpha', imageRef: 'aaa:latest' }),
    ]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: 'Image' }))

    const names = screen.getAllByText(/^(zeta|alpha)$/).map((el) => el.textContent)
    expect(names).toEqual(['alpha', 'zeta'])
  })

  it('filters rows by the text query across name/image/project fields', () => {
    const containers = [
      makeContainer({ containerId: 'a', containerName: 'web-one' }),
      makeContainer({ containerId: 'b', containerName: 'other' }),
    ]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    fireEvent.change(screen.getByPlaceholderText('Filter updates…'), { target: { value: 'web' } })

    expect(screen.getByText('web-one')).toBeInTheDocument()
    expect(screen.queryByText('other')).not.toBeInTheDocument()
    expect(screen.getByText('1 of 2 updates available')).toBeInTheDocument()
  })

  it('shows truncated local → remote digest for cached updates only', () => {
    const cached = makeCachedUpdate({ containerId: 'c2', containerName: 'api' })
    const normal = makeContainer({ containerId: 'c1', containerName: 'web' })
    setCheckUpdates({ data: { updates: [normal, cached], fromCache: true } })
    render(<UpdatesTab />)

    const expected = `${cached.localDigest.substring(0, 12)} → ${cached.remoteDigest.substring(0, 12)}`
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('links to the stack when the container belongs to one', () => {
    const containers = [makeContainer({ containerId: 'a', stackId: 'stack1', projectName: 'myproject' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    expect(screen.getByRole('link', { name: 'myproject' })).toHaveAttribute('href', '/stacks/stack1')
  })

  it('shows standalone label when the container has no stack or project', () => {
    const containers = [makeContainer({ containerId: 'a', stackId: '', projectName: '' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    expect(screen.getByText('standalone')).toBeInTheDocument()
  })
})

// ─── Auto-update / backup policy resolution ────────────────────────────────────

describe('UpdatesTab — policy resolution', () => {
  it('prefers a container-level policy over a stack-level policy', () => {
    const containers = [makeContainer({ containerId: 'c1', stackId: 'stack1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockAutoUpdatePolicies.mockReturnValue({
      data: {
        policies: [
          makePolicy({ targetType: 'container', targetId: 'c1', enabled: true }),
          makePolicy({ targetType: 'stack', targetId: 'stack1', enabled: false }),
        ],
      },
    })
    render(<UpdatesTab />)

    const toggle = screen.getByTestId('auto-update-toggle-c1')
    expect(toggle).toHaveAttribute('data-target-type', 'container')
    expect(toggle).toHaveAttribute('data-enabled', 'true')
  })

  it('falls back to a stack-level policy when no container-level policy exists', () => {
    const containers = [makeContainer({ containerId: 'c1', stackId: 'stack1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockAutoUpdatePolicies.mockReturnValue({
      data: { policies: [makePolicy({ targetType: 'stack', targetId: 'stack1', enabled: true })] },
    })
    render(<UpdatesTab />)

    const toggle = screen.getByTestId('auto-update-toggle-stack1')
    expect(toggle).toHaveAttribute('data-target-type', 'stack')
  })

  it('defaults to a disabled container-level toggle when no policy matches', () => {
    const containers = [makeContainer({ containerId: 'c1', stackId: 'stack1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    const toggle = screen.getByTestId('auto-update-toggle-c1')
    expect(toggle).toHaveAttribute('data-target-type', 'container')
    expect(toggle).toHaveAttribute('data-enabled', 'false')
  })

  it('shows the backup toggle for a stack when only a stack-level (or no) policy applies', () => {
    const containers = [makeContainer({ containerId: 'c1', stackId: 'stack1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    expect(screen.getByTestId('backup-toggle-stack1')).toBeInTheDocument()
  })

  it('hides the backup toggle when a container-level policy exists', () => {
    const containers = [makeContainer({ containerId: 'c1', stackId: 'stack1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockAutoUpdatePolicies.mockReturnValue({
      data: { policies: [makePolicy({ targetType: 'container', targetId: 'c1' })] },
    })
    render(<UpdatesTab />)

    expect(screen.queryByTestId('backup-toggle-stack1')).not.toBeInTheDocument()
  })

  it('hides the backup toggle for standalone containers with no stack', () => {
    const containers = [makeContainer({ containerId: 'c1', stackId: '' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    expect(screen.queryByTestId(/^backup-toggle-/)).not.toBeInTheDocument()
  })
})

// ─── Update action + toast messaging ───────────────────────────────────────────

describe('UpdatesTab — triggering an update', () => {
  it('queues an update and shows a "queued for update and restart" toast for a running container', () => {
    const containers = [makeContainer({ containerId: 'c1', containerName: 'web', state: 'running' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockUpdateMutate.mockImplementation((_id, { onSuccess }) => onSuccess())
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /^update & restart$/i }))

    expect(mockUpdateMutate).toHaveBeenCalledWith('c1', expect.any(Object))
    expect(toast.info).toHaveBeenCalledWith('web queued for update and restart')
  })

  it('shows a "queued for update" toast (no restart wording) for a stopped container', () => {
    const containers = [makeContainer({ containerId: 'c1', containerName: 'web', state: 'stopped' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockUpdateMutate.mockImplementation((_id, { onSuccess }) => onSuccess())
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /^update$/i }))

    expect(toast.info).toHaveBeenCalledWith('web queued for update')
  })

  it('shows a classified error toast when the update mutation fails', () => {
    const containers = [makeContainer({ containerId: 'c1', containerName: 'web', state: 'stopped' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockUpdateMutate.mockImplementation((_id, { onError }) => onError(new Error('boom')))
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /^update$/i }))

    expect(toast.error).toHaveBeenCalled()
  })
})

// ─── Expand/collapse job log ───────────────────────────────────────────────────

describe('UpdatesTab — expandable job log row', () => {
  it('expands to show the job log when a job exists and the row is toggled', () => {
    const containers = [makeContainer({ containerId: 'c1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    mockJobForContainer = (id: string) => (id === 'c1' ? { id: 'job1', status: 'pulling' } : undefined)
    render(<UpdatesTab />)

    fireEvent.click(screen.getByRole('button', { name: /expand log/i }))

    expect(screen.getByTestId('update-job-log-job1')).toBeInTheDocument()
  })

  it('does not render an expand control when there is no job for the container', () => {
    const containers = [makeContainer({ containerId: 'c1' })]
    setCheckUpdates({ data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    expect(screen.queryByRole('button', { name: /expand log/i })).not.toBeInTheDocument()
  })
})

// ─── Tab switching ──────────────────────────────────────────────────────────────

describe('UpdatesTab — tab switching', () => {
  it('hydrates update jobs on mount', () => {
    render(<UpdatesTab />)
    expect(mockUpdateJobsHydrate).toHaveBeenCalled()
  })

  it('switches to the Update Log tab and renders it', () => {
    render(<UpdatesTab />)

    // Radix Tabs activates on mousedown (not click) — see TabsTrigger's onMouseDown handler.
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Update Log' }))

    expect(screen.getByTestId('update-log-tab')).toBeInTheDocument()
  })
})

// ─── agent-os-4gve: the error guard must key on DATA PRESENCE ────────────────

/**
 * agent-os-4gve, the 13th site of the agent-os-wczm class.
 *
 * The guard read `isError && !fromCache`. `fromCache` is a PAYLOAD FIELD
 * (`updateData?.fromCache ?? false` in useUpdatesData), read off data TanStack
 * RETAINS across a failed refetch — so it answers "did the payload say it came
 * from cache", not "is there anything to show". The cache reaches a populated
 * list carrying `fromCache:false` through a different writer than this panel's
 * own query: useResources' checkUpdatesRefresh mutation calls
 * `queryClient.setQueryData(queryKeys.resources.updates(), data)` with the
 * response from checkUpdates(TRUE) on its no-scheduler branch. From that state
 * one focus refetch that 500s replaced a populated table with the error card.
 *
 * `!hasData` is NOT the fix and was rejected: `hasData` is `updates.length > 0`,
 * list non-emptiness, which is the same category error in a new spelling and
 * turns the `updates: []` + `fromCache: true` arm above red.
 */
describe('UpdatesTab — a failed REFETCH must not discard a populated table (agent-os-4gve)', () => {
  it('keeps the populated updates table when the query errors with data already held', () => {
    const containers = [makeContainer({ containerId: 'a', containerName: 'zeta' })]
    // Exactly the writer's shape: a full list with fromCache:false.
    setCheckUpdates({ isError: true, data: { updates: containers, fromCache: false } })
    render(<UpdatesTab />)

    // The row the server already sent is still on screen...
    expect(screen.getByText('zeta')).toBeInTheDocument()
    // ...and the error card has NOT replaced it.
    expect(screen.queryByText('Failed to Check for Updates')).not.toBeInTheDocument()
    // ...and the failure is reported rather than swallowed.
    expect(
      screen.getByText(/Could not refresh the available updates\. The values shown are the last ones the server sent\./),
    ).toBeInTheDocument()
    expect(screen.queryByText(/check them before saving/)).not.toBeInTheDocument()
  })

  /**
   * PRESERVATION CONTROL. Cannot fail first — it passes against the old guard
   * too — so it is pinned by mutation evidence against the FIXED code instead
   * (the agent-os-erfc lesson). A first-load failure has no data to protect and
   * must still surrender the whole panel to the error card.
   */
  it('still shows the error card on a first-load failure with no data', () => {
    setCheckUpdates({ isError: true })
    render(<UpdatesTab />)

    expect(screen.getByText('Failed to Check for Updates')).toBeInTheDocument()
    expect(screen.queryByText(/Could not refresh the available updates/)).not.toBeInTheDocument()
  })

  /**
   * The other half of UpdatesTab.test.tsx:231. That arm pins that an errored
   * query serving a cached EMPTY list does not show the error card; it was
   * green before this change and stays green. What it never pinned is that the
   * operator is told the refresh failed at all — without this the panel says
   * "no updates" while the check actually errored.
   */
  it('reports the refresh failure when errored while serving a cached empty list', () => {
    setCheckUpdates({ isError: true, data: { updates: [], fromCache: true } })
    render(<UpdatesTab />)

    expect(screen.queryByText('Failed to Check for Updates')).not.toBeInTheDocument()
    expect(screen.getByText(/Could not refresh the available updates/)).toBeInTheDocument()
  })
})
