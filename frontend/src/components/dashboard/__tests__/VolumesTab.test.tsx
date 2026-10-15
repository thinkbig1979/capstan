import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { VolumesTab } from '../VolumesTab'
import type { DockerVolume } from '@/types'

/**
 * DashboardPage.test.tsx:92 replaces this tab with an empty div (0/40
 * statements, agent-os-m1mu). Rendered for real here with only the API layer
 * mocked.
 */

const mockGetVolumes = vi.fn()
const mockDeleteVolume = vi.fn()
const mockPruneVolumes = vi.fn()
const mockVolumePruneImpact = vi.fn()
const mockVolumePruneScope = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    volumes: (...a: unknown[]) => mockGetVolumes(...a),
    deleteVolume: (...a: unknown[]) => mockDeleteVolume(...a),
    pruneVolumes: (...a: unknown[]) => mockPruneVolumes(...a),
    volumePruneImpact: (...a: unknown[]) => mockVolumePruneImpact(...a),
    volumePruneScope: (...a: unknown[]) => mockVolumePruneScope(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const volume = (over: Partial<DockerVolume> = {}): DockerVolume => ({
  name: 'app-data',
  driver: 'local',
  mountpoint: '/var/lib/docker/volumes/app-data/_data',
  size: 1024 * 1024,
  sizeKnown: true,
  inUse: false,
  inUseKnown: true,
  created: '2026-08-01T00:00:00Z',
  stack: '',
  ...over,
})

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const renderTab = () => render(<VolumesTab />, { wrapper: createWrapper() })

beforeEach(() => {
  vi.clearAllMocks()
  mockGetVolumes.mockResolvedValue([volume()])
  mockDeleteVolume.mockResolvedValue({})
  mockPruneVolumes.mockResolvedValue({ outcome: 'no_change', reason: 'nothing to prune', details: { deleted: [] } })
  mockVolumePruneImpact.mockResolvedValue({ stacks: [], uncheckedStacks: [] })
  mockVolumePruneScope.mockResolvedValue({ removesAllUnused: false })
})

describe('VolumesTab — loading and empty', () => {
  it('shows skeletons while loading', () => {
    mockGetVolumes.mockReturnValue(new Promise(() => {}))
    renderTab()

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('shows the empty state when there are no volumes', async () => {
    mockGetVolumes.mockResolvedValue([])
    renderTab()

    expect(await screen.findByText('No Volumes')).toBeInTheDocument()
  })
})

describe('VolumesTab — the table', () => {
  it('shows name, driver and mountpoint', async () => {
    renderTab()

    expect(await screen.findByText('app-data')).toBeInTheDocument()
    expect(screen.getByText('local')).toBeInTheDocument()
    expect(screen.getByText('/var/lib/docker/volumes/app-data/_data')).toBeInTheDocument()
  })

  // agent-os-gkgr: Docker's -1 "not computed" arrives as sizeKnown false. It
  // reads "size unknown", the Host page's wording, never a byte count.
  it('says "size unknown" when the size was never calculated', async () => {
    mockGetVolumes.mockResolvedValue([volume({ sizeKnown: false, size: 0 })])
    renderTab()

    await screen.findByText('app-data')
    const sizeCell = screen.getAllByRole('row')[1].querySelectorAll('td')[2]
    expect(sizeCell).toHaveTextContent(/^size unknown$/)
  })

  it('badges the owning stack, or a dash when the volume is unattached', async () => {
    mockGetVolumes.mockResolvedValue([
      volume({ name: 'owned', stack: 'web' }),
      volume({ name: 'orphan', stack: '' }),
    ])
    renderTab()

    expect(await screen.findByText('web')).toBeInTheDocument()
    expect(screen.getByText('-')).toBeInTheDocument()
  })

  it('counts the volumes in the header', async () => {
    mockGetVolumes.mockResolvedValue([volume({ name: 'a' }), volume({ name: 'b' })])
    renderTab()

    expect(await screen.findByText('2 volumes')).toBeInTheDocument()
  })
})

describe('VolumesTab — filtering and sorting', () => {
  const TWO = [
    volume({ name: 'zebra', driver: 'local', stack: 'web', size: 100 }),
    volume({ name: 'alpha', driver: 'nfs', stack: 'api', size: 900 }),
  ]

  it('filters on the volume name', async () => {
    mockGetVolumes.mockResolvedValue(TWO)
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter volumes…'), {
      target: { value: 'alpha' },
    })

    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.queryByText('zebra')).not.toBeInTheDocument()
  })

  it('filters on the driver and on the owning stack', async () => {
    mockGetVolumes.mockResolvedValue(TWO)
    renderTab()

    const filter = await screen.findByPlaceholderText('Filter volumes…')

    fireEvent.change(filter, { target: { value: 'nfs' } })
    expect(screen.getByText('alpha')).toBeInTheDocument()

    fireEvent.change(filter, { target: { value: 'web' } })
    expect(screen.getByText('zebra')).toBeInTheDocument()
    expect(screen.queryByText('alpha')).not.toBeInTheDocument()
  })

  it('sorts by name by default', async () => {
    mockGetVolumes.mockResolvedValue(TWO)
    renderTab()

    await screen.findByText('alpha')
    expect(screen.getAllByRole('row').slice(1)[0]).toHaveTextContent('alpha')
  })

  it('re-sorts by size, largest first', async () => {
    mockGetVolumes.mockResolvedValue(TWO)
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Size' }))

    expect(screen.getAllByRole('row').slice(1)[0]).toHaveTextContent('alpha')
  })

  it('sorts a volume of unknown size after every known size', async () => {
    mockGetVolumes.mockResolvedValue([
      volume({ name: 'known-small', size: 10 }),
      volume({ name: 'unknown', size: 0, sizeKnown: false }),
      volume({ name: 'known-empty', size: 0 }),
      volume({ name: 'known-big', size: 999 }),
    ])
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Size' }))

    const order = screen.getAllByRole('row').slice(1).map((r) => r.querySelector('td')?.textContent)
    expect(order).toEqual(['known-big', 'known-small', 'known-empty', 'unknown'])
  })

  it('switches the count display while filtering', async () => {
    mockGetVolumes.mockResolvedValue(TWO)
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter volumes…'), {
      target: { value: 'alpha' },
    })

    expect(screen.getByText('1 of 2 volumes')).toBeInTheDocument()
  })
})

describe('VolumesTab — deleting', () => {
  it('warns that the data goes too, then removes', async () => {
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Remove volume app-data' }))

    expect(await screen.findByText('Remove Volume "app-data"?')).toBeInTheDocument()
    expect(screen.getByText(/its data will be permanently removed/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(mockDeleteVolume).toHaveBeenCalledWith('app-data', false))
  })

  it('deletes nothing when the confirmation is dismissed', async () => {
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Remove volume app-data' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByText(/Remove Volume/)).not.toBeInTheDocument())
    expect(mockDeleteVolume).not.toHaveBeenCalled()
  })

  it('disables removal for a volume still in use, and says why', async () => {
    mockGetVolumes.mockResolvedValue([volume({ name: 'busy', inUse: true })])
    renderTab()

    const button = await screen.findByRole('button', {
      name: 'Remove volume busy (in use, cannot be removed)',
    })
    expect(button).toBeDisabled()

    fireEvent.click(button)
    expect(mockDeleteVolume).not.toHaveBeenCalled()
  })

  // agent-os-gkgr: inUse false with inUseKnown false means nobody counted the
  // volume's references, so it must not be offered as safe to delete.
  it('disables removal when the in-use state is unknown, and says why', async () => {
    mockGetVolumes.mockResolvedValue([volume({ name: 'unchecked', inUse: false, inUseKnown: false })])
    renderTab()

    const button = await screen.findByRole('button', {
      name: 'Remove volume unchecked (in-use state unknown, cannot be removed)',
    })
    expect(button).toBeDisabled()

    fireEvent.click(button)
    expect(mockDeleteVolume).not.toHaveBeenCalled()
    expect(screen.queryByText(/Remove Volume/)).not.toBeInTheDocument()
  })
})

describe('VolumesTab — a failed Docker read is not an empty host (agent-os-v824)', () => {
  const serverFault = { status: 500, code: 'DOCKER_ERROR', message: 'docker unavailable' }

  function renderWithClient() {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
    })
    render(<VolumesTab />, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    })
    return queryClient
  }

  it('first load fails: an error with Retry, NOT "No Volumes"', async () => {
    mockGetVolumes.mockRejectedValue(serverFault)
    const queryClient = renderWithClient()
    await waitFor(() =>
      expect(queryClient.getQueryState(['resources', 'volumes'])?.status).toBe('error'),
    )

    // Pre-fix this read "No Volumes": a failed Docker read shown as fact.
    expect(screen.queryByText('No Volumes')).not.toBeInTheDocument()
    expect(screen.getByText('Could not load the volume list.')).toBeInTheDocument()

    mockGetVolumes.mockResolvedValue([volume()])
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('app-data')).toBeInTheDocument()
    expect(screen.queryByText('Could not load the volume list.')).not.toBeInTheDocument()
  })

  it('a refetch fails over a loaded list: keeps the list AND says so', async () => {
    const queryClient = renderWithClient()
    expect(await screen.findByText('app-data')).toBeInTheDocument()

    mockGetVolumes.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(queryClient.getQueryState(['resources', 'volumes'])?.status).toBe('error'),
    )

    // Retention first, so a red below means RETAINED AND UNDISCLOSED.
    expect(screen.getByText('app-data')).toBeInTheDocument()
    expect(
      screen.getByText('Could not refresh the volume list. The values shown are the last ones the server sent.'),
    ).toBeInTheDocument()
  })

  it('a host that really has none still shows the empty state, with no error', async () => {
    mockGetVolumes.mockResolvedValue([])
    renderWithClient()

    expect(await screen.findByText('No Volumes')).toBeInTheDocument()
    expect(screen.queryByText(/Could not (load|refresh)/)).not.toBeInTheDocument()
  })
})

// agent-os-8d8a: "all unused" removes every volume no container holds,
// including a removed (down) stack's database. The dialog names those stacks
// and asks for the word "prune" before it removes anything.
describe('VolumesTab — prune warns about stacks that would lose volumes', () => {
  async function openPruneWithAll(all: boolean) {
    const user = userEvent.setup()
    renderTab()
    await screen.findByText('app-data')
    await user.click(screen.getByRole('button', { name: /^prune$/i }))
    await screen.findByText('Prune Unused Volumes?')
    if (all) await user.click(screen.getByRole('switch'))
    return user
  }
  const confirmButton = () => screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
  const typedBox = () => screen.queryByRole('textbox', { name: /type prune to confirm/i })

  it('names the affected stack and keeps Prune disabled until "prune" is typed', async () => {
    mockVolumePruneImpact.mockResolvedValue({
      stacks: [{ stack: 'shop', items: ['pgdata', 'shop-archive'] }],
      uncheckedStacks: [],
    })
    const user = await openPruneWithAll(true)

    expect(await screen.findByText(/removed \(down\) stacks would lose these volumes/i)).toBeInTheDocument()
    expect(screen.getByText('shop')).toBeInTheDocument()
    expect(screen.getByText(/pgdata, shop-archive/)).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()

    await user.type(typedBox()!, 'prun')
    expect(confirmButton()).toBeDisabled()
    await user.type(typedBox()!, 'e')
    expect(confirmButton()).toBeEnabled()

    await user.click(confirmButton())
    await waitFor(() => expect(mockPruneVolumes).toHaveBeenCalledTimes(1))
    expect(mockPruneVolumes.mock.calls[0][0]).toEqual({ all: true, until: undefined })
  })

  it('says no stack is affected and needs no typing when none is', async () => {
    await openPruneWithAll(true)

    expect(await screen.findByText(/no stack loses volumes to this prune/i)).toBeInTheDocument()
    expect(typedBox()).not.toBeInTheDocument()
    expect(confirmButton()).toBeEnabled()
  })

  it('says it could not check, and still requires typing, when the check fails', async () => {
    mockVolumePruneImpact.mockRejectedValue(new Error('500'))
    const user = await openPruneWithAll(true)

    expect(await screen.findByText(/could not check which stacks this prune affects/i)).toBeInTheDocument()
    expect(screen.queryByText(/no stack loses/i)).not.toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
    await user.type(typedBox()!, 'prune')
    expect(confirmButton()).toBeEnabled()
  })

  it('names a stack whose compose file could not be read, and requires typing', async () => {
    mockVolumePruneImpact.mockResolvedValue({ stacks: [], uncheckedStacks: ['broken'] })
    await openPruneWithAll(true)

    expect(await screen.findByText(/could not read the compose file of: broken/i)).toBeInTheDocument()
    expect(screen.queryByText(/no stack loses/i)).not.toBeInTheDocument()
    expect(typedBox()).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
  })

  it('does not check or ask for typing when only anonymous volumes are pruned', async () => {
    await openPruneWithAll(false)
    expect(await screen.findByText(/by default only anonymous volumes are removed/i)).toBeInTheDocument()

    expect(typedBox()).not.toBeInTheDocument()
    expect(confirmButton()).toBeEnabled()
    expect(mockVolumePruneImpact).not.toHaveBeenCalled()
  })
})

// agent-os-ny0c: below Docker API 1.42 (an old Engine, or DOCKER_API_VERSION
// pinned) the daemon turns the default prune into "every unused volume", so
// the stack check runs without "all unused" too, and the copy says so.
describe('VolumesTab — default prune on a Docker API below 1.42', () => {
  async function openDefaultPrune() {
    const user = userEvent.setup()
    renderTab()
    await screen.findByText('app-data')
    await user.click(screen.getByRole('button', { name: /^prune$/i }))
    await screen.findByText('Prune Unused Volumes?')
    return user
  }
  const confirmButton = () => screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
  const typedBox = () => screen.queryByRole('textbox', { name: /type prune to confirm/i })

  it('names the stacks the default prune would empty, and requires typing', async () => {
    mockVolumePruneScope.mockResolvedValue({ removesAllUnused: true })
    mockVolumePruneImpact.mockResolvedValue({ stacks: [{ stack: 'shop', items: ['pgdata'] }], uncheckedStacks: [] })
    await openDefaultPrune()

    expect(await screen.findByText(/removed \(down\) stacks would lose these volumes/i)).toBeInTheDocument()
    expect(screen.getByText('shop')).toBeInTheDocument()
    expect(mockVolumePruneImpact).toHaveBeenCalled()
    expect(typedBox()).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
    expect(
      screen.getByText('This Docker version (API below 1.42) removes every volume not used by a container, named ones included, even without "all unused".'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/only anonymous volumes are removed/i)).not.toBeInTheDocument()
  })

  it('runs the check when the Docker version could not be read', async () => {
    mockVolumePruneScope.mockRejectedValue(new Error('500'))
    mockVolumePruneImpact.mockResolvedValue({ stacks: [{ stack: 'shop', items: ['pgdata'] }], uncheckedStacks: [] })
    await openDefaultPrune()

    expect(await screen.findByText(/removed \(down\) stacks would lose these volumes/i)).toBeInTheDocument()
    expect(mockVolumePruneImpact).toHaveBeenCalled()
    expect(confirmButton()).toBeDisabled()
    expect(screen.queryByText(/by default only anonymous volumes are removed/i)).not.toBeInTheDocument()
  })
})

// The adversary review of ae78352: the gates below held, but no test pinned
// them (agent-os-8d8a round 2).
describe('VolumesTab — the prune check gates Confirm at every step', () => {
  const impactKey = ['resources', 'volumes', 'prune-impact']
  const shop = { stack: 'shop', items: ['pgdata'] }
  const confirmButton = () => screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
  const typedBox = () => screen.queryByRole('textbox', { name: /type prune to confirm/i })

  function renderWithClient() {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
    })
    render(<VolumesTab />, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    })
    return queryClient
  }

  async function openWithAll(user: ReturnType<typeof userEvent.setup>, firstOpen = true) {
    if (firstOpen) await screen.findByText('app-data')
    await user.click(screen.getAllByRole('button', { name: /^prune$/i })[0])
    await screen.findByText('Prune Unused Volumes?')
    if (firstOpen) await user.click(screen.getByRole('switch'))
  }

  it('keeps Prune disabled while the check is still running', async () => {
    mockVolumePruneImpact.mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    renderWithClient()
    await openWithAll(user)

    expect(await screen.findByText(/checking which stacks this prune affects/i)).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
  })

  it('does not show the last answer as current while a reopened dialog re-checks', async () => {
    const user = userEvent.setup()
    renderWithClient()
    await openWithAll(user)
    expect(await screen.findByText(/no stack loses volumes/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    mockVolumePruneImpact.mockReturnValue(new Promise(() => {}))
    await openWithAll(user, false)

    expect(await screen.findByText(/checking which stacks this prune affects/i)).toBeInTheDocument()
    expect(screen.queryByText(/no stack loses volumes/i)).not.toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
  })

  it('clears the typed word when a re-check adds a stack', async () => {
    mockVolumePruneImpact.mockResolvedValue({ stacks: [shop], uncheckedStacks: [] })
    const user = userEvent.setup()
    const queryClient = renderWithClient()
    await openWithAll(user)
    await screen.findByText(/pgdata/)
    await user.type(typedBox()!, 'prune')
    expect(confirmButton()).toBeEnabled()

    // A window-focus refetch, say, finds a second stack.
    mockVolumePruneImpact.mockResolvedValue({ stacks: [shop, { stack: 'blog', items: ['blog_db'] }], uncheckedStacks: [] })
    await act(() => queryClient.refetchQueries({ queryKey: impactKey }))

    expect(await screen.findByText(/blog_db/)).toBeInTheDocument()
    expect(typedBox()).toHaveValue('')
    expect(confirmButton()).toBeDisabled()
  })

  it('clears the typed word when a re-check fails', async () => {
    mockVolumePruneImpact.mockResolvedValue({ stacks: [shop], uncheckedStacks: [] })
    const user = userEvent.setup()
    const queryClient = renderWithClient()
    await openWithAll(user)
    await screen.findByText(/pgdata/)
    await user.type(typedBox()!, 'prune')
    expect(confirmButton()).toBeEnabled()

    mockVolumePruneImpact.mockRejectedValue(new Error('503'))
    await act(() => queryClient.refetchQueries({ queryKey: impactKey }))

    expect(await screen.findByText(/could not check which stacks this prune affects/i)).toBeInTheDocument()
    expect(typedBox()).toHaveValue('')
    expect(confirmButton()).toBeDisabled()
  })
})
