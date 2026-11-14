import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router'
import type { HostSummary } from '@/types/generated-services'

// agent-os-mi4r AC8: /?tab=host#automatic-cleanup scrolls the REAL panel into
// view after the host summary has loaded. Content above the panel fills in
// when the summary arrives and pushes it down, so a scroll fired before that
// lands short. HostTab.test.tsx stubs the panel; this file does not.

const hostSummary = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    hostSummary: (...args: unknown[]) => hostSummary(...args),
    getCleanupPolicy: () =>
      Promise.resolve({
        enabled: true,
        minAgeHours: 168,
        intervalHours: 24,
        categories: { danglingImages: true, unusedImages: false, buildCache: true, containers: false, volumes: false, networks: false },
        alertThresholdBytes: 0,
        nextRunAt: new Date(Date.now() + 3_600_000).toISOString(),
        minAllowedAgeHours: 6,
        minAllowedIntervalHours: 2,
      }),
    getCleanupHistory: () => Promise.resolve({ runs: [], limit: 20 }),
    previewCleanup: () => Promise.resolve({ items: [], totalBytes: 0 }),
  },
  dashboardApi: { stats: () => Promise.resolve({ totalContainers: 1 }) },
}))

vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => ({
    containers: [],
    aggregates: {},
    latestMetrics: {},
    isConnected: false,
    ws: { status: 'open' },
  }),
}))
vi.mock('@/components/host/FreeUpSpaceDialog', () => ({ FreeUpSpaceDialog: () => null }))

import { HostTab } from '../HostTab'

const GiB = 1024 ** 3

function makeSummary(): HostSummary {
  return {
    disk: { total: 10 * GiB, inUse: 8 * GiB, reclaimable: 2 * GiB, reclaimableAll: 3 * GiB, volumesSizeKnown: true, containersSizeKnown: true },
    containers: { count: 2, running: 2, stopped: 0, reclaimableCount: 0, reclaimableBytes: 0, reclaimableBytesKnown: true },
    images: { count: 1, inUse: 1, inUseKnown: true, bytes: GiB, reclaimableCount: 0, reclaimableBytes: 0 },
    volumes: { count: 1, attached: 1, attachedKnown: true, bytes: GiB, sizeKnown: true, reclaimableCount: 0, reclaimableBytes: 0 },
    networks: { count: 1, builtIn: 1, stackOwned: 0, reclaimableCount: 0 },
    buildCache: { count: 0, bytes: 0, reclaimableBytes: 0 },
    measuredAt: '2026-11-14T10:00:00Z',
  }
}

// The panel's distance from the top of <main>: 300 px while the skeleton is
// above it, 900 px once the disk section and the five cards have rendered.
let summaryLoaded = false
const scrolls: { to: number; summaryLoaded: boolean }[] = []

beforeEach(() => {
  summaryLoaded = false
  scrolls.length = 0
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const top = this.id === 'automatic-cleanup' ? (summaryLoaded ? 900 : 300) : 0
    return { top, left: 0, right: 0, bottom: top, width: 0, height: 0, x: 0, y: top, toJSON: () => ({}) } as DOMRect
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

function renderAt(url: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[url]}>
        <main data-testid="scroller">
          <Routes>
            <Route path="/" element={<HostTab />} />
          </Routes>
        </main>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  const main = screen.getByTestId('scroller')
  let scrollTop = 0
  Object.defineProperty(main, 'scrollTop', {
    configurable: true,
    get: () => scrollTop,
    set: (v: number) => {
      scrollTop = v
      scrolls.push({ to: v, summaryLoaded })
    },
  })
  return view
}

describe('HostTab #automatic-cleanup (AC8)', () => {
  it('scrolls to the panel after the summary has loaded, not before', async () => {
    let resolve!: (s: HostSummary) => void
    hostSummary.mockReturnValue(new Promise<HostSummary>((r) => (resolve = r)))
    renderAt('/?tab=host#automatic-cleanup')

    // The policy loads first; the summary is still pending.
    await screen.findByText('On')
    expect(scrolls).toEqual([])

    summaryLoaded = true
    resolve(makeSummary())
    await screen.findByTestId('host-disk-bar')
    await waitFor(() => expect(scrolls).toEqual([{ to: 900, summaryLoaded: true }]))
  })

  it('does not scroll without the hash', async () => {
    hostSummary.mockResolvedValue(makeSummary())
    summaryLoaded = true
    renderAt('/?tab=host')
    await screen.findByTestId('host-disk-bar')
    await screen.findByText('On')
    expect(scrolls).toEqual([])
  })
})
