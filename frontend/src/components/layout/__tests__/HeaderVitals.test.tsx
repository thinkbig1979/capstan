import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { HeaderVitals } from '../HeaderVitals'
import { GiB, makeHostSummary } from '@/test/hostSummary'
import { QueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/query-keys'

/**
 * agent-os-p9e1. GET /dashboard/stats omits diskUsage when Docker's disk usage
 * could not be read. The header's disk vital must say so, not "0 B", while a
 * host genuinely using no disk still reads 0 B.
 */

const { statsMock, hostSummaryMock } = vi.hoisted(() => ({ statsMock: vi.fn(), hostSummaryMock: vi.fn() }))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    dashboardApi: { ...actual.dashboardApi, stats: statsMock },
    resourcesApi: { ...actual.resourcesApi, hostSummary: hostSummaryMock },
  }
})

// Disconnected metrics stream by default, so the only vital is the one under
// test; the AC7 test connects it.
const metrics = vi.hoisted(() => ({ value: { aggregates: {} as Record<string, number>, isConnected: false } }))
vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => metrics.value,
}))

const diskVital = async () => (await screen.findByTestId('header-vital-disk')).textContent

const knownDisk = { total: 30 * GiB, images: 0, containers: 0, volumes: 0, buildCache: 0, volumesSizeKnown: true, containersSizeKnown: true }

beforeEach(() => {
  vi.clearAllMocks()
  metrics.value = { aggregates: {}, isConnected: false }
  hostSummaryMock.mockResolvedValue(makeHostSummary())
})

describe('HeaderVitals — disk', () => {
  it('shows disk as unavailable, not 0 B, when the server omitted disk usage', async () => {
    statsMock.mockResolvedValue({ totalStacks: 2 })
    renderWithProviders(<HeaderVitals />)

    // Precondition: the stats loaded and the vital rendered.
    expect(await screen.findByTestId('header-vitals')).toBeInTheDocument()
    expect(await diskVital()).toBe('diskunavailable')
  })

  it('still shows 0 B for a host genuinely using no disk', async () => {
    statsMock.mockResolvedValue({
      totalStacks: 2,
      diskUsage: { total: 0, images: 0, containers: 0, volumes: 0, buildCache: 0, volumesSizeKnown: true, containersSizeKnown: true },
    })
    renderWithProviders(<HeaderVitals />)

    expect(await screen.findByTestId('header-vitals')).toBeInTheDocument()
    expect(await diskVital()).toBe('disk0 B')
  })

  // agent-os-rwik: a size Docker did not measure (-1) is left out of the
  // total, so the total is a floor and must say "at least".
  it.each([
    ['a volume', { volumesSizeKnown: false, containersSizeKnown: true }],
    ['a container', { volumesSizeKnown: true, containersSizeKnown: false }],
  ])('reads "at least" when Docker could not size %s', async (_what, flags) => {
    statsMock.mockResolvedValue({
      totalStacks: 2,
      diskUsage: { total: 2048, images: 0, containers: 1024, volumes: 1024, buildCache: 0, ...flags },
    })
    renderWithProviders(<HeaderVitals />)

    expect(await screen.findByTestId('header-vitals')).toBeInTheDocument()
    expect(await diskVital()).toBe('diskat least 2.00 KB')
  })
})

describe('HeaderVitals — every vital links to the Host tab (agent-os-mi4r)', () => {
  it('puts cpu, mem and disk inside one link to /?tab=host', async () => {
    metrics.value = {
      isConnected: true,
      aggregates: { totalCpuPercent: 23, totalMemPercent: 40, totalMemUsage: 6 * GiB },
    }
    statsMock.mockResolvedValue({ totalStacks: 2, diskUsage: knownDisk })
    renderWithProviders(<HeaderVitals />)

    const link = await screen.findByTestId('header-vitals-link')
    expect(link.tagName).toBe('A')
    expect(link).toHaveAttribute('href', '/?tab=host')
    for (const vital of ['cpu', 'mem', 'disk']) {
      expect(link).toContainElement(await screen.findByTestId(`header-vital-${vital}`))
    }
  })

  it('is a link to /?tab=host showing the total and an "unused" chip', async () => {
    statsMock.mockResolvedValue({ totalStacks: 2, diskUsage: knownDisk })
    hostSummaryMock.mockResolvedValue(makeHostSummary({ disk: { total: 30 * GiB, reclaimable: 14.9 * GiB } }))
    renderWithProviders(<HeaderVitals />)

    const link = await screen.findByTestId('header-vitals-link')
    expect(link.tagName).toBe('A')
    expect(link).toHaveAttribute('href', '/?tab=host')
    expect(await diskVital()).toBe('disk30.00 GB')
    expect(await screen.findByTestId('header-disk-unused')).toHaveTextContent('14.90 GB unused')
    expect(link).toContainElement(screen.getByTestId('header-disk-unused'))
  })

  it('shows no chip when nothing is reclaimable', async () => {
    statsMock.mockResolvedValue({ totalStacks: 2, diskUsage: knownDisk })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    renderWithProviders(<HeaderVitals />, { queryClient })

    await screen.findByTestId('header-vitals-link')
    await waitFor(() => expect(queryClient.getQueryState(queryKeys.resources.hostSummary())?.status).toBe('success'))
    expect(screen.queryByTestId('header-disk-unused')).not.toBeInTheDocument()
  })

  it('keeps "unavailable" and the link when the server omitted disk usage', async () => {
    statsMock.mockResolvedValue({ totalStacks: 2 })
    renderWithProviders(<HeaderVitals />)

    expect(await screen.findByTestId('header-vitals-link')).toHaveAttribute('href', '/?tab=host')
    expect(await diskVital()).toBe('diskunavailable')
  })

  // AC15: the tooltip never presents an unmeasured volume size as known.
  it('says "size unknown" in the tooltip when Docker could not size every volume', async () => {
    statsMock.mockResolvedValue({ totalStacks: 2, diskUsage: knownDisk })
    hostSummaryMock.mockResolvedValue(makeHostSummary({ disk: { volumesSizeKnown: false }, volumes: { sizeKnown: false } }))
    renderWithProviders(<HeaderVitals />)

    const link = await screen.findByTestId('header-vitals-link')
    await waitFor(() => expect(link.getAttribute('title')).toContain('Volumes: size unknown'))
  })

  it('has no "size unknown" in the tooltip when every volume was sized', async () => {
    statsMock.mockResolvedValue({ totalStacks: 2, diskUsage: knownDisk })
    hostSummaryMock.mockResolvedValue(makeHostSummary({ disk: { reclaimable: GiB } }))
    renderWithProviders(<HeaderVitals />)

    await screen.findByTestId('header-disk-unused')
    expect(screen.getByTestId('header-vitals-link').getAttribute('title')).not.toContain('size unknown')
  })
})
