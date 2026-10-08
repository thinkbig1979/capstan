/**
 * agent-os-7nqa. The follow-up to the settings pages (agent-os-cg26): a tab or
 * panel that FIRST renders while the browser is offline has a query whose
 * fetchStatus is 'paused'. TanStack's isLoading = isPending && isFetching, so
 * isLoading is false with data still undefined and every `if (isLoading)
 * spinner` branch fell through to a claim about a read that never ran: "No
 * Images", "No backup history", "Stack Not Found", "Failed to load diff", "No
 * Scan Data Available".
 *
 * Two arms per component, on the same component:
 *  - offline: a loading view, and not the false claim (nothing was attempted);
 *  - online with a REJECTING read: the failure view, and no loading view, so the
 *    fix did not swallow a real first-load failure.
 * The pages (DashboardPage, StackPage) carry their offline arm in their own
 * test files.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest'
import type { ReactElement } from 'react'
import { screen, cleanup, fireEvent } from '@testing-library/react'
import { onlineManager, QueryClient } from '@tanstack/react-query'
import { renderWithProviders } from '@/test/utils'
import { queryKeys } from '@/lib/query-keys'
import type { BackupSnapshot } from '@/types'
import { ImagesTab } from '../dashboard/ImagesTab'
import { VolumesTab } from '../dashboard/VolumesTab'
import { NetworksTab } from '../dashboard/NetworksTab'
import { BuildCacheTab } from '../dashboard/BuildCacheTab'
import { BackupHistoryTab } from '../dashboard/BackupHistoryTab'
import { BackupStatusCard } from '../dashboard/BackupStatusCard'
import { UpdateLogTab } from '../dashboard/UpdateLogTab'
import { UpdatesTab } from '../dashboard/UpdatesTab'
import { StackUpdatesTab } from '../stack/StackUpdatesTab'
import { GitHistory } from '../git/GitHistory'
import { DiffViewer } from '../git/DiffViewer'
import { EnvEditor } from '../stack/EnvEditor'
import { ComposeEditor } from '../stack/ComposeEditor'
import { BackupsTab } from '../stack/BackupsTab'
import { Sidebar } from '../layout/Sidebar'

// Every API method the components under test can reach rejects. Offline, none of
// them is ever called (the queries are paused), which is the point of the
// offline arm.
vi.mock('@/lib/api', async (importOriginal) => {
  const failing = () =>
    new Proxy({}, { get: () => () => Promise.reject(new Error('boom')) })
  return {
    ...(await importOriginal<typeof import('@/lib/api')>()),
    resourcesApi: failing(),
    settingsApi: failing(),
    autoUpdateApi: failing(),
    directoryConfigApi: failing(),
    directoriesApi: failing(),
    backupApi: failing(),
    versionApi: failing(),
    gitApi: failing(),
    stacksApi: failing(),
  }
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// The sidebar reads matchMedia, which jsdom lacks.
beforeAll(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
})

const LOADING = '.animate-spin, .animate-pulse'
const hasLoading = (container: HTMLElement, loadingText?: RegExp) =>
  container.querySelector(LOADING) !== null ||
  (loadingText !== undefined && screen.queryAllByText(loadingText).length > 0)

interface Case {
  name: string
  element: ReactElement
  /** What the component says if it treats the paused read as an answer (omit when it makes no claim). */
  falseClaim?: RegExp
  /** The loading view's text, for a component that shows words instead of a spinner. */
  loadingText?: RegExp
  /** What it says when its first read really failed (omit when it has no error view). */
  failure?: RegExp
}

const CASES: Case[] = [
  { name: 'ImagesTab', element: <ImagesTab />, falseClaim: /No Images/, failure: /Could not load the image list/ },
  { name: 'VolumesTab', element: <VolumesTab />, falseClaim: /No Volumes/, failure: /Could not load/ },
  { name: 'NetworksTab', element: <NetworksTab />, falseClaim: /No Networks/, failure: /Could not load/ },
  { name: 'BuildCacheTab', element: <BuildCacheTab />, falseClaim: /Build cache is empty/, failure: /Could not load/ },
  { name: 'BackupHistoryTab', element: <BackupHistoryTab />, falseClaim: /No backup history/, failure: /Failed to Load Backup History/ },
  { name: 'BackupStatusCard', element: <BackupStatusCard />, falseClaim: /Back up now/ },
  { name: 'UpdateLogTab', element: <UpdateLogTab />, falseClaim: /No Update History/, failure: /Failed to Load Update History/ },
  { name: 'UpdatesTab (available updates)', element: <UpdatesTab />, falseClaim: /No Scan Data Available/, failure: /Failed to Check for Updates/ },
  { name: 'StackUpdatesTab', element: <StackUpdatesTab stackId="s1" />, falseClaim: /No Update History/, failure: /Failed to Load Update History/ },
  { name: 'GitHistory', element: <GitHistory stackId="s1" />, falseClaim: /Showing 0 commits|No commits in this repository/, loadingText: /Loading git history/, failure: /Failed to load git history/ },
  { name: 'DiffViewer', element: <DiffViewer stackId="s1" commitHash="abc" />, falseClaim: /Failed to load diff/, loadingText: /Loading diff/, failure: /Failed to load diff/ },
  { name: 'EnvEditor', element: <EnvEditor stackId="s1" />, falseClaim: /No environment file found/, loadingText: /Loading/, failure: /Failed to load environment file/ },
  { name: 'ComposeEditor', element: <ComposeEditor stackId="s1" />, loadingText: /Loading compose file/ },
  { name: 'BackupsTab', element: <BackupsTab stackId="s1" />, falseClaim: /No snapshots yet|No backup runs yet/, failure: /Failed to load snapshots/ },
  { name: 'Sidebar', element: <Sidebar />, falseClaim: /No stacks found/, loadingText: /Loading\.\.\./, failure: /Could not load the stack list/ },
]

afterEach(() => {
  cleanup()
  onlineManager.setOnline(true)
})

describe.each(CASES)('$name — first render', ({ element, falseClaim, loadingText, failure }) => {
  it('offline: shows a loading view, not the false claim', () => {
    onlineManager.setOnline(false)
    const { container } = renderWithProviders(element)

    expect(hasLoading(container, loadingText)).toBe(true)
    if (falseClaim) expect(screen.queryAllByText(falseClaim)).toHaveLength(0)
  })

  it('online with a rejecting read: leaves the loading view for the failure', async () => {
    const { container } = renderWithProviders(element)

    if (failure) expect((await screen.findAllByText(failure)).length).toBeGreaterThan(0)
    else await new Promise((r) => setTimeout(r, 50))
    expect(hasLoading(container, loadingText)).toBe(false)
  })
})

// A gated query (`enabled: !!arg`) that is DISABLED is 'idle', not paused: it
// never loads, so the component must fall through rather than spin forever.
// This is the arm that tells isFirstLoad from plain isPending.
describe('enabled-gated queries that are disabled fall through, offline or not', () => {
  it.each([true, false])('DiffViewer with no commit hash (online=%s)', (online) => {
    onlineManager.setOnline(online)
    const { container } = renderWithProviders(<DiffViewer stackId="s1" commitHash="" />)

    expect(hasLoading(container)).toBe(false)
    expect(screen.queryByText('Loading diff...')).not.toBeInTheDocument()
  })

  it.each([true, false])('BackupsTab with no stack id (online=%s)', (online) => {
    onlineManager.setOnline(online)
    renderWithProviders(<BackupsTab stackId="" />)

    expect(screen.queryByText(/Loading snapshots/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Loading runs/)).not.toBeInTheDocument()
  })
})

describe('BackupsTab snapshot preview — offline over a loaded snapshot list', () => {
  const SNAPSHOT: BackupSnapshot = {
    id: 'abcdef1234567890',
    shortId: 'abcdef12',
    time: '2026-10-01T10:00:00Z',
    hostname: 'host',
    tags: [],
    paths: [],
    sizeBytes: 1024,
  }

  it('shows "Loading preview…" while the preview read is paused', async () => {
    onlineManager.setOnline(false)
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(queryKeys.backup.snapshots('s1'), [SNAPSHOT])
    renderWithProviders(<BackupsTab stackId="s1" />, { queryClient })

    fireEvent.click(await screen.findByRole('button', { name: 'Show preview' }))

    expect(screen.getByText(/Loading preview/)).toBeInTheDocument()
  })
})
