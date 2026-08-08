import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
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

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    volumes: (...a: unknown[]) => mockGetVolumes(...a),
    deleteVolume: (...a: unknown[]) => mockDeleteVolume(...a),
    pruneVolumes: vi.fn().mockResolvedValue({}),
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

  it('shows an em dash when the size was never calculated', async () => {
    mockGetVolumes.mockResolvedValue([volume({ sizeKnown: false, size: 0 })])
    renderTab()

    expect(await screen.findByTitle('Size not calculated')).toHaveTextContent('—')
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
})
