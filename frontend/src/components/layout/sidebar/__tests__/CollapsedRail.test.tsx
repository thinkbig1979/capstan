import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { GiB, makeHostSummary } from '@/test/hostSummary'
import { queryKeys } from '@/lib/query-keys'
import { CollapsedRail } from '../CollapsedRail'

const { hostSummaryMock } = vi.hoisted(() => ({ hostSummaryMock: vi.fn() }))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return { ...actual, resourcesApi: { ...actual.resourcesApi, hostSummary: hostSummaryMock } }
})

function renderRail(path = '/') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <CollapsedRail stackCount={2} updateCount={0} onToggleSidebar={() => {}} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return queryClient
}

const hostLink = () => screen.getByRole('link', { name: 'Docker host' })

beforeEach(() => {
  vi.clearAllMocks()
})

describe('CollapsedRail — Docker host', () => {
  it('renders a link named "Docker host" to /host', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary())
    renderRail()

    expect(hostLink()).toHaveAttribute('href', '/host')
    expect(hostLink()).not.toHaveAttribute('aria-current')
  })

  it('shows a dot when something is reclaimable', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary({ disk: { reclaimable: 3 * GiB } }))
    renderRail()

    expect(await screen.findByTestId('rail-host-dot')).toBeInTheDocument()
    expect(hostLink()).toHaveAttribute('title', 'Docker host, 3.00 GB unused')
  })

  it('shows no dot when nothing is reclaimable', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary())
    const queryClient = renderRail()

    await waitFor(() => expect(queryClient.getQueryState(queryKeys.resources.hostSummary())?.status).toBe('success'))
    expect(screen.queryByTestId('rail-host-dot')).not.toBeInTheDocument()
  })

  it('shows no dot when the summary could not be read', async () => {
    hostSummaryMock.mockRejectedValue(new Error('503'))
    const queryClient = renderRail()

    await waitFor(() => expect(queryClient.getQueryState(queryKeys.resources.hostSummary())?.status).toBe('error'))
    expect(screen.queryByTestId('rail-host-dot')).not.toBeInTheDocument()
  })

  it('drops the dot when a refetch fails after a successful load', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary({ disk: { reclaimable: 3 * GiB } }))
    const queryClient = renderRail()
    await screen.findByTestId('rail-host-dot')

    hostSummaryMock.mockRejectedValue(new Error('503'))
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: queryKeys.resources.hostSummary() })
    })
    await waitFor(() => expect(queryClient.getQueryState(queryKeys.resources.hostSummary())?.status).toBe('error'))
    // Precondition: the stale summary is still in the cache.
    expect(queryClient.getQueryData(queryKeys.resources.hostSummary())).toBeDefined()
    expect(screen.queryByTestId('rail-host-dot')).not.toBeInTheDocument()
  })

  it.each(['/host', '/host/images'])('marks the link current on %s', (path) => {
    hostSummaryMock.mockResolvedValue(makeHostSummary())
    renderRail(path)

    expect(hostLink()).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Dashboard' })).not.toHaveAttribute('aria-current')
  })

  it('does not mark the link current on a lookalike path', () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary())
    renderRail('/hostile')

    expect(hostLink()).not.toHaveAttribute('aria-current')
  })
})
