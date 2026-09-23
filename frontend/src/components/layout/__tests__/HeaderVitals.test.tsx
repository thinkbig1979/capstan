import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { HeaderVitals } from '../HeaderVitals'

/**
 * agent-os-p9e1. GET /dashboard/stats omits diskUsage when Docker's disk usage
 * could not be read. The header's disk vital must say so, not "0 B", while a
 * host genuinely using no disk still reads 0 B.
 */

const { statsMock } = vi.hoisted(() => ({ statsMock: vi.fn() }))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return { ...actual, dashboardApi: { ...actual.dashboardApi, stats: statsMock } }
})

// Disconnected metrics stream, so the only vital is the one under test.
vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => ({ aggregates: {}, isConnected: false }),
}))

const diskVital = async () => (await screen.findByTitle('Host disk')).textContent

beforeEach(() => {
  vi.clearAllMocks()
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
      diskUsage: { total: 0, images: 0, containers: 0, volumes: 0, buildCache: 0 },
    })
    renderWithProviders(<HeaderVitals />)

    expect(await screen.findByTestId('header-vitals')).toBeInTheDocument()
    expect(await diskVital()).toBe('disk0 B')
  })
})
