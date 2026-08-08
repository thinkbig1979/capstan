import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
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

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    networks: (...a: unknown[]) => mockGetNetworks(...a),
    deleteNetwork: (...a: unknown[]) => mockDeleteNetwork(...a),
    pruneNetworks: vi.fn().mockResolvedValue({}),
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

  it('uses singular wording for a single attached container', async () => {
    mockGetNetworks.mockResolvedValue([network({ name: 'busy', containers: 1 })])
    renderTab()

    expect(
      await screen.findByRole('button', { name: /In use by 1 container(?!s)/ }),
    ).toBeDisabled()
  })
})
