import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { GiB, makeHostSummary } from '@/test/hostSummary'
import { useUIStore } from '@/stores/uiStore'
import { queryKeys } from '@/lib/query-keys'
import { AttentionStrip } from '../AttentionStrip'

const { hostSummaryMock, policyMock } = vi.hoisted(() => ({
  hostSummaryMock: vi.fn(),
  policyMock: vi.fn(),
}))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    resourcesApi: {
      ...actual.resourcesApi,
      checkUpdates: vi.fn().mockResolvedValue({ updates: [] }),
      hostSummary: hostSummaryMock,
      getCleanupPolicy: policyMock,
    },
  }
})

const DAY = 24 * 60 * 60 * 1000
const NOW = new Date('2026-11-13T10:00:00Z').getTime()

const policy = (alertThresholdBytes: number) => ({
  enabled: false,
  intervalHours: 24,
  minAgeHours: 168,
  alertThresholdBytes,
  categories: {},
})

function Where() {
  const l = useLocation()
  return <div data-testid="where">{l.pathname + l.search}</div>
}

function renderStrip(queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  const ui = (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route
            path="*"
            element={
              <>
                <AttentionStrip stacks={[]} onShowUpdates={() => {}} onFilterStopped={() => {}} onFilterError={() => {}} />
                <Where />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
  return { ...render(ui), queryClient }
}

const reclaimable = (bytes: number) => makeHostSummary({ disk: { total: 30 * GiB, reclaimable: bytes } })

/** Waits until both queries settled, so a hidden alert is hidden for the rule, not for loading. */
async function settled(queryClient: QueryClient) {
  await waitFor(() => {
    expect(queryClient.getQueryState(queryKeys.resources.hostSummary())?.status).not.toBe('pending')
    expect(queryClient.getQueryState(queryKeys.settings.dockerCleanup())?.status).not.toBe('pending')
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
  useUIStore.setState({ hostSpaceAlertDismissedUntil: null })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('AttentionStrip — host space alert', () => {
  it('shows at the threshold, with the reclaimable figure', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(5 * GiB))
    policyMock.mockResolvedValue(policy(5 * GiB))
    renderStrip()

    const alert = await screen.findByTestId('attention-host-space')
    expect(alert).toHaveTextContent('5.00 GB of unused Docker data on this server')
  })

  it('shows above the threshold', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(14.9 * GiB))
    policyMock.mockResolvedValue(policy(5 * GiB))
    renderStrip()

    expect(await screen.findByTestId('attention-host-space')).toHaveTextContent('14.90 GB of unused')
  })

  it('is hidden below the threshold', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(5 * GiB - 1))
    policyMock.mockResolvedValue(policy(5 * GiB))
    const { queryClient } = renderStrip()

    await settled(queryClient)
    expect(screen.queryByTestId('attention-host-space')).not.toBeInTheDocument()
  })

  it('is hidden when the threshold is 0 (alert turned off)', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(50 * GiB))
    policyMock.mockResolvedValue(policy(0))
    const { queryClient } = renderStrip()

    await settled(queryClient)
    expect(screen.queryByTestId('attention-host-space')).not.toBeInTheDocument()
  })

  it('is hidden while the policy is loading', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(50 * GiB))
    policyMock.mockReturnValue(new Promise(() => {}))
    const { queryClient } = renderStrip()

    await waitFor(() => expect(queryClient.getQueryState(queryKeys.resources.hostSummary())?.status).toBe('success'))
    expect(screen.queryByTestId('attention-host-space')).not.toBeInTheDocument()
  })

  it('is hidden when the policy could not be read', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(50 * GiB))
    policyMock.mockRejectedValue(new Error('500'))
    const { queryClient } = renderStrip()

    await settled(queryClient)
    expect(queryClient.getQueryState(queryKeys.settings.dockerCleanup())?.status).toBe('error')
    expect(screen.queryByTestId('attention-host-space')).not.toBeInTheDocument()
  })

  it('keeps the last-known threshold through a failed policy refetch', async () => {
    hostSummaryMock.mockResolvedValue(reclaimable(50 * GiB))
    policyMock.mockResolvedValue(policy(5 * GiB))
    const { queryClient } = renderStrip()
    await screen.findByTestId('attention-host-space')

    policyMock.mockRejectedValue(new Error('500'))
    await queryClient.refetchQueries({ queryKey: queryKeys.settings.dockerCleanup() })
    await waitFor(() => expect(queryClient.getQueryState(queryKeys.settings.dockerCleanup())?.status).toBe('error'))
    expect(screen.getByTestId('attention-host-space')).toBeInTheDocument()
  })

  it('is hidden after dismiss and shown again after 7 days', async () => {
    const user = userEvent.setup()
    hostSummaryMock.mockResolvedValue(reclaimable(50 * GiB))
    policyMock.mockResolvedValue(policy(5 * GiB))
    const { queryClient, unmount } = renderStrip()

    await user.click(await screen.findByRole('button', { name: 'Dismiss for 7 days' }))
    expect(screen.queryByTestId('attention-host-space')).not.toBeInTheDocument()
    expect(useUIStore.getState().hostSpaceAlertDismissedUntil).toBe(NOW + 7 * DAY)

    // Still dismissed a minute before the 7 days are up, on a fresh mount
    // (e.g. a reload) that reads the clock anew.
    vi.setSystemTime(NOW + 7 * DAY - 60_000)
    unmount()
    renderStrip(queryClient)
    expect(screen.getByTestId('where')).toBeInTheDocument()
    expect(queryClient.getQueryData(queryKeys.resources.hostSummary())).toBeDefined()
    expect(screen.queryByTestId('attention-host-space')).not.toBeInTheDocument()

    // Past the 7 days, the 60 s summary poll (a changed figure) brings it back
    // without a reload.

    vi.setSystemTime(NOW + 7 * DAY + 1)
    hostSummaryMock.mockResolvedValue(reclaimable(52 * GiB))
    await queryClient.refetchQueries({ queryKey: queryKeys.resources.hostSummary() })
    expect(await screen.findByTestId('attention-host-space')).toHaveTextContent('52.00 GB of unused')
  })

  it('"Free up space" navigates to /?tab=host&free=1', async () => {
    const user = userEvent.setup()
    hostSummaryMock.mockResolvedValue(reclaimable(50 * GiB))
    policyMock.mockResolvedValue(policy(5 * GiB))
    renderStrip()

    await user.click(await screen.findByRole('button', { name: /free up space/i }))
    expect(screen.getByTestId('where')).toHaveTextContent('/?tab=host&free=1')
  })
})
