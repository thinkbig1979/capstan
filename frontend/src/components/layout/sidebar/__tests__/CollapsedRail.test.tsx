import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { GiB, makeHostSummary } from '@/test/hostSummary'
import { CollapsedRail } from '../CollapsedRail'

// agent-os-mi4r: the Docker host is the dashboard's Host tab, so the rail has
// no Server link and no reclaimable dot; the Dashboard icon leads to the tabs.

const { hostSummaryMock } = vi.hoisted(() => ({ hostSummaryMock: vi.fn() }))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return { ...actual, resourcesApi: { ...actual.resourcesApi, hostSummary: hostSummaryMock } }
})

function renderRail(path = '/') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <CollapsedRail stackCount={2} updateCount={0} onToggleSidebar={() => {}} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return view.container
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('CollapsedRail — no Docker host link', () => {
  it('links Dashboard and Settings only, even with space to free', async () => {
    hostSummaryMock.mockResolvedValue(makeHostSummary({ disk: { reclaimable: 3 * GiB } }))
    const container = renderRail()

    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings')
    expect(screen.queryByRole('link', { name: /Docker host/ })).not.toBeInTheDocument()
    expect(screen.queryByTestId('rail-host-dot')).not.toBeInTheDocument()
    expect(container.querySelector('a[href^="/host"]')).toBeNull()
  })
})
