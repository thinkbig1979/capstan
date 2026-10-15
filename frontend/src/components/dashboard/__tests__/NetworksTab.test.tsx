import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { NetworksTab } from '../NetworksTab'
import type { DockerNetwork } from '@/types'

/**
 * DashboardPage.test.tsx:95 replaces this tab with an empty div (0/54
 * statements, agent-os-m1mu). CreateNetworkDialog is stubbed — it is a
 * separate module with its own hole and its own tests to come; what matters
 * here is that NetworksTab opens it.
 */

const mockGetNetworks = vi.fn()
const mockDeleteNetwork = vi.fn()
const mockPruneNetworks = vi.fn()
const mockNetworkPruneImpact = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    networks: (...a: unknown[]) => mockGetNetworks(...a),
    deleteNetwork: (...a: unknown[]) => mockDeleteNetwork(...a),
    pruneNetworks: (...a: unknown[]) => mockPruneNetworks(...a),
    networkPruneImpact: (...a: unknown[]) => mockNetworkPruneImpact(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

vi.mock('../CreateNetworkDialog', () => ({
  CreateNetworkDialog: ({ open }: { open: boolean }) =>
    open ? <div data-testid="create-network-dialog" /> : null,
}))

const network = (over: Partial<DockerNetwork> = {}): DockerNetwork => ({
  id: 'abcdef0123456789abcdef0123456789',
  name: 'web_default',
  driver: 'bridge',
  scope: 'local',
  internal: false,
  containers: 0,
  containersKnown: true,
  labels: [],
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

const renderTab = () => render(<NetworksTab />, { wrapper: createWrapper() })

beforeEach(() => {
  vi.clearAllMocks()
  mockGetNetworks.mockResolvedValue([network()])
  mockDeleteNetwork.mockResolvedValue({})
  mockPruneNetworks.mockResolvedValue({ outcome: 'no_change', reason: 'nothing to prune', details: { deleted: [] } })
  mockNetworkPruneImpact.mockResolvedValue({ stacks: [], uncheckedStacks: [] })
})

describe('NetworksTab — loading and empty', () => {
  it('shows skeletons while loading', () => {
    mockGetNetworks.mockReturnValue(new Promise(() => {}))
    renderTab()

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('offers to create one from the empty state', async () => {
    mockGetNetworks.mockResolvedValue([])
    renderTab()

    expect(await screen.findByText('No Networks')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Create Network/ }))
    expect(screen.getByTestId('create-network-dialog')).toBeInTheDocument()
  })
})

describe('NetworksTab — the table', () => {
  it('shows name, truncated id, driver and scope', async () => {
    renderTab()

    expect(await screen.findByText('web_default')).toBeInTheDocument()
    expect(screen.getByText('abcdef0123456789abc')).toBeInTheDocument()
    expect(screen.getByText('bridge')).toBeInTheDocument()
    expect(screen.getByText('local')).toBeInTheDocument()
  })

  it('marks an internal network Yes and an external one No', async () => {
    mockGetNetworks.mockResolvedValue([
      network({ id: 'a', name: 'inner', internal: true }),
      network({ id: 'b', name: 'outer', internal: false }),
    ])
    renderTab()

    expect(await screen.findByText('Yes')).toBeInTheDocument()
    expect(screen.getByText('No')).toBeInTheDocument()
  })

  it('counts the networks in the header', async () => {
    mockGetNetworks.mockResolvedValue([network({ id: 'a' }), network({ id: 'b', name: 'other' })])
    renderTab()

    expect(await screen.findByText('2 networks')).toBeInTheDocument()
  })

  it('opens the create dialog from the toolbar', async () => {
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: /Create/ }))

    expect(screen.getByTestId('create-network-dialog')).toBeInTheDocument()
  })
})

describe('NetworksTab — filtering and sorting', () => {
  const TWO = [
    network({ id: 'a', name: 'zebra', driver: 'bridge', scope: 'local', stack: 'web' }),
    network({ id: 'b', name: 'alpha', driver: 'overlay', scope: 'swarm', stack: 'api' }),
  ]

  it('filters on name, driver, scope and stack', async () => {
    mockGetNetworks.mockResolvedValue(TWO)
    renderTab()

    const filter = await screen.findByPlaceholderText('Filter networks…')

    for (const [term, expected, hidden] of [
      ['alpha', 'alpha', 'zebra'],
      ['overlay', 'alpha', 'zebra'],
      ['swarm', 'alpha', 'zebra'],
      ['web', 'zebra', 'alpha'],
    ] as const) {
      fireEvent.change(filter, { target: { value: term } })
      expect(screen.getByText(expected)).toBeInTheDocument()
      expect(screen.queryByText(hidden)).not.toBeInTheDocument()
    }
  })

  it('sorts by name by default and by container count on demand', async () => {
    mockGetNetworks.mockResolvedValue([
      network({ id: 'a', name: 'zebra', containers: 5 }),
      network({ id: 'b', name: 'alpha', containers: 1 }),
    ])
    renderTab()

    await screen.findByText('alpha')
    expect(screen.getAllByRole('row').slice(1)[0]).toHaveTextContent('alpha')

    fireEvent.click(screen.getByRole('button', { name: 'Containers' }))
    expect(screen.getAllByRole('row').slice(1)[0]).toHaveTextContent('zebra')
  })

  it('switches the count display while filtering', async () => {
    mockGetNetworks.mockResolvedValue(TWO)
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter networks…'), {
      target: { value: 'alpha' },
    })

    expect(screen.getByText('1 of 2 networks')).toBeInTheDocument()
  })
})

describe('NetworksTab — deleting', () => {
  it('confirms, then removes by id', async () => {
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: /Remove network web_default/ }))
    expect(await screen.findByText('Remove Network "web_default"?')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() =>
      expect(mockDeleteNetwork).toHaveBeenCalledWith('abcdef0123456789abcdef0123456789'),
    )
  })

  it('deletes nothing when the confirmation is dismissed', async () => {
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: /Remove network/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByText(/Remove Network/)).not.toBeInTheDocument())
    expect(mockDeleteNetwork).not.toHaveBeenCalled()
  })

  it.each(['bridge', 'host', 'none'])(
    'refuses to remove the %s system network',
    async (name) => {
      mockGetNetworks.mockResolvedValue([network({ id: 'sys', name })])
      renderTab()

      const button = await screen.findByRole('button', {
        name: new RegExp(`Remove network ${name}`),
      })
      expect(button).toBeDisabled()
      expect(button).toHaveAccessibleName(new RegExp("System network can't be removed"))
    },
  )

  it('refuses to remove a network that still has containers, and counts them', async () => {
    mockGetNetworks.mockResolvedValue([network({ name: 'busy', containers: 2 })])
    renderTab()

    const button = await screen.findByRole('button', { name: /Remove network busy/ })
    expect(button).toBeDisabled()
    expect(button).toHaveAccessibleName(/In use by 2 containers/)
  })

  // agent-os-gkgr (p8sm F1): a failed inspect arrives as containersKnown false
  // with containers 0. It must not read as "0 attached" or offer Delete.
  it('refuses to remove a network whose attachments are unknown, and says so', async () => {
    mockGetNetworks.mockResolvedValue([network({ name: 'unchecked', containers: 0, containersKnown: false })])
    renderTab()

    const button = await screen.findByRole('button', { name: /Remove network unchecked/ })
    expect(button).toBeDisabled()
    expect(button).toHaveAccessibleName(/Attached containers unknown/)

    const containersCell = screen.getAllByRole('row')[1].querySelectorAll('td')[5]
    expect(containersCell).toHaveTextContent(/^unknown$/)
  })

  it('sorts a network with an unknown count after every known count', async () => {
    mockGetNetworks.mockResolvedValue([
      network({ id: 'a', name: 'one', containers: 1 }),
      network({ id: 'b', name: 'unchecked', containers: 0, containersKnown: false }),
      network({ id: 'c', name: 'none', containers: 0 }),
    ])
    renderTab()

    await screen.findByText('one')
    fireEvent.click(screen.getByRole('button', { name: 'Containers' }))
    const order = screen.getAllByRole('row').slice(1).map((r) => r.querySelector('td')?.textContent)
    expect(order).toEqual(['one', 'none', 'unchecked'])
  })

  it('uses singular wording for a single attached container', async () => {
    mockGetNetworks.mockResolvedValue([network({ name: 'busy', containers: 1 })])
    renderTab()

    expect(
      await screen.findByRole('button', { name: /In use by 1 container(?!s)/ }),
    ).toBeDisabled()
  })
})

describe('NetworksTab — a failed Docker read is not an empty host (agent-os-v824)', () => {
  const serverFault = { status: 500, code: 'DOCKER_ERROR', message: 'docker unavailable' }

  function renderWithClient() {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
    })
    render(<NetworksTab />, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    })
    return queryClient
  }

  it('first load fails: an error with Retry, NOT "No Networks"', async () => {
    mockGetNetworks.mockRejectedValue(serverFault)
    const queryClient = renderWithClient()
    await waitFor(() =>
      expect(queryClient.getQueryState(['resources', 'networks'])?.status).toBe('error'),
    )

    // Pre-fix this read "No Networks": a failed Docker read shown as fact.
    expect(screen.queryByText('No Networks')).not.toBeInTheDocument()
    expect(screen.getByText('Could not load the network list.')).toBeInTheDocument()

    mockGetNetworks.mockResolvedValue([network()])
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('web_default')).toBeInTheDocument()
    expect(screen.queryByText('Could not load the network list.')).not.toBeInTheDocument()
  })

  it('a refetch fails over a loaded list: keeps the list AND says so', async () => {
    const queryClient = renderWithClient()
    expect(await screen.findByText('web_default')).toBeInTheDocument()

    mockGetNetworks.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(queryClient.getQueryState(['resources', 'networks'])?.status).toBe('error'),
    )

    // Retention first, so a red below means RETAINED AND UNDISCLOSED.
    expect(screen.getByText('web_default')).toBeInTheDocument()
    expect(
      screen.getByText('Could not refresh the network list. The values shown are the last ones the server sent.'),
    ).toBeInTheDocument()
  })

  it('a host that really has none still shows the empty state, with no error', async () => {
    mockGetNetworks.mockResolvedValue([])
    renderWithClient()

    expect(await screen.findByText('No Networks')).toBeInTheDocument()
    expect(screen.queryByText(/Could not (load|refresh)/)).not.toBeInTheDocument()
  })
})

// agent-os-8d8a: network prune removes every network no RUNNING container is
// attached to, so a stopped stack loses its networks as well as a removed one
// (an external network's loss fails the next up). The dialog names those
// stacks and asks for the word "prune".
describe('NetworksTab — prune warns about stacks that would lose networks', () => {
  async function openPrune() {
    const user = userEvent.setup()
    renderTab()
    await screen.findByText('web_default')
    await user.click(screen.getByRole('button', { name: /^prune$/i }))
    await screen.findByText('Prune Unused Networks?')
    return user
  }
  const confirmButton = () => screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
  const typedBox = () => screen.queryByRole('textbox', { name: /type prune to confirm/i })

  it('names the affected stack as stopped or removed, and keeps Prune disabled until "prune" is typed', async () => {
    mockNetworkPruneImpact.mockResolvedValue({
      stacks: [{ stack: 'shop', items: ['proxy', 'shop_default'] }],
      uncheckedStacks: [],
    })
    const user = await openPrune()

    // Pinned copy: "down" alone would be false, a STOPPED stack loses its
    // networks too (OBSERVED on Docker API 1.45).
    expect(await screen.findByText(/stopped or removed stacks would lose these networks/i)).toBeInTheDocument()
    expect(screen.getByText('shop')).toBeInTheDocument()
    expect(screen.getByText(/proxy, shop_default/)).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()

    await user.type(typedBox()!, 'prune')
    expect(confirmButton()).toBeEnabled()
    await user.click(confirmButton())
    await waitFor(() => expect(mockPruneNetworks).toHaveBeenCalledTimes(1))
  })

  it('says no stack is affected and needs no typing when none is', async () => {
    await openPrune()

    expect(await screen.findByText(/no stack loses networks to this prune/i)).toBeInTheDocument()
    expect(typedBox()).not.toBeInTheDocument()
    expect(confirmButton()).toBeEnabled()
  })

  it('says it could not check, and still requires typing, when the check fails', async () => {
    mockNetworkPruneImpact.mockRejectedValue(new Error('503'))
    const user = await openPrune()

    expect(await screen.findByText(/could not check which stacks this prune affects/i)).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
    await user.type(typedBox()!, 'prune')
    expect(confirmButton()).toBeEnabled()
  })
})
