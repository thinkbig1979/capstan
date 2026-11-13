import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, screen, within } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/query-keys'
import { renderWithProviders } from '@/test/utils'
import { GiB, makeHostSummary } from '@/test/hostSummary'
import { HostTiles } from '../HostTiles'

const { hostSummaryMock } = vi.hoisted(() => ({ hostSummaryMock: vi.fn() }))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return { ...actual, resourcesApi: { ...actual.resourcesApi, hostSummary: hostSummaryMock } }
})

const tile = (view: string) => screen.getByTestId(`host-tile-${view}`)
const value = (view: string) => within(tile(view)).getByTestId('host-tile-value').textContent
const sub = (view: string) => within(tile(view)).queryByTestId('host-tile-sub')?.textContent

beforeEach(() => {
  vi.clearAllMocks()
})

describe('HostTiles', () => {
  it('renders five tiles linking to their host views, each with label, value and sub-line, plus the overview link', async () => {
    hostSummaryMock.mockResolvedValue(
      makeHostSummary({
        containers: { count: 9, reclaimableCount: 3 },
        images: { count: 40, bytes: 21 * GiB, reclaimableCount: 17, reclaimableBytes: 14.9 * GiB },
        volumes: { count: 2, attached: 2, bytes: GiB },
        networks: { count: 5, reclaimableCount: 1 },
        buildCache: { count: 4, bytes: 2 * GiB, reclaimableBytes: 0 },
      }),
    )
    renderWithProviders(<HostTiles />)

    const tiles = await screen.findByTestId('host-tiles')
    await screen.findByText('21.00 GB')

    const links = within(tiles).getAllByRole('link')
    expect(links).toHaveLength(6)
    expect(links.map((l) => l.getAttribute('href')).sort()).toEqual([
      '/host',
      '/host/build-cache',
      '/host/containers',
      '/host/images',
      '/host/networks',
      '/host/volumes',
    ])
    expect(within(tiles).getByRole('link', { name: /open host overview/i })).toHaveAttribute('href', '/host')

    expect(within(tile('containers')).getByText('Containers')).toBeInTheDocument()
    expect(value('containers')).toBe('9')
    expect(sub('containers')).toBe('3 leftovers')
    expect(value('images')).toBe('21.00 GB')
    expect(sub('images')).toBe('14.90 GB unused')
    expect(value('volumes')).toBe('1.00 GB')
    expect(sub('volumes')).toBe('2 volumes · all in use')
    expect(value('networks')).toBe('5')
    expect(sub('networks')).toBe('1 unused')
    expect(value('build-cache')).toBe('2.00 GB')
    expect(sub('build-cache')).toBe('all in use')
  })

  // AC15: an unmeasured volume is not "0 B" (the tile once read the sum unconditionally).
  it('reads "size unknown" for volumes when Docker could not size every volume', async () => {
    hostSummaryMock.mockResolvedValue(
      makeHostSummary({ volumes: { count: 3, bytes: 0, sizeKnown: false, reclaimableCount: 1 } }),
    )
    renderWithProviders(<HostTiles />)

    expect(await screen.findByText('size unknown')).toBeInTheDocument()
    expect(value('volumes')).toBe('size unknown')
    expect(sub('volumes')).toBe('3 volumes · 1 unattached')
  })

  // agent-os-p9e1 class, moved from HostStrip ("omitted stats"): a failed read
  // is "unavailable", never a 0 nobody measured.
  it('shows unavailable, not 0, when the host summary could not be read', async () => {
    hostSummaryMock.mockRejectedValue(new Error('503'))
    renderWithProviders(<HostTiles />)

    // Precondition: the tiles rendered past the loading state.
    expect(await screen.findAllByText('unavailable')).toHaveLength(5)
    for (const view of ['containers', 'images', 'volumes', 'networks', 'build-cache']) {
      expect(value(view)).toBe('unavailable')
    }
    expect(screen.queryByText('0')).not.toBeInTheDocument()
    expect(screen.queryByText('0 B')).not.toBeInTheDocument()
  })

  // A failed refetch keeps the last figures, and says they may be stale.
  it('keeps the last figures with a could-not-refresh notice when a refetch fails', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary({ images: { bytes: 21 * GiB, reclaimableCount: 2, reclaimableBytes: 9 * GiB } }))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    renderWithProviders(<HostTiles />, { queryClient })
    await screen.findByText('21.00 GB')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    hostSummaryMock.mockRejectedValue(new Error('503'))
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: queryKeys.resources.hostSummary() })
    })

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not refresh the Docker host summary')
    expect(value('images')).toBe('21.00 GB')
    expect(sub('images')).toBe('9.00 GB unused')
  })

  it('still shows 0 for a genuinely empty host', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary())
    renderWithProviders(<HostTiles />)

    await screen.findAllByText('all in use')
    expect(value('containers')).toBe('0')
    expect(value('images')).toBe('0 B')
    expect(value('volumes')).toBe('0 B')
    expect(sub('volumes')).toBe('0 volumes · all in use')
    expect(screen.queryByText('unavailable')).not.toBeInTheDocument()
  })

  it('reads the exact volumes figure when every volume was sized', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary({ volumes: { count: 1, bytes: 3 * 1024 ** 2 } }))
    renderWithProviders(<HostTiles />)

    expect(await screen.findByText('3.00 MB')).toBeInTheDocument()
    expect(value('volumes')).toBe('3.00 MB')
  })
})
