import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UpdateScheduleContent } from '../UpdateScheduleContent'

/**
 * The two ways this panel can be wrong about its own data, pinned together
 * because they are one design question: how does this component tell the
 * operator that what it is showing is degraded?
 *
 *   agent-os-ptiq — the whole form is STALE and says nothing. The values are
 *     real values the server did send, so retaining them is right; the
 *     disclosure is what was missing, and this is a form the operator SAVES
 *     FROM.
 *   agent-os-xppj — the statistics are not stale, they were FABRICATED: the
 *     backend could not count and sent zeros anyway. With the server fixed to
 *     omit the block, the panel must say the counts are unavailable rather than
 *     silently dropping the section.
 *
 * Kept out of UpdateScheduleContent.test.tsx because that file mocks
 * settingsApi.getUpdates with a single beforeEach resolve; both arms here need
 * the mock to change BETWEEN renders.
 */

const mockGetUpdates = vi.fn()
const mockUpdateUpdates = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getUpdates: (...args: unknown[]) => mockGetUpdates(...args),
    updateUpdates: (...args: unknown[]) => mockUpdateUpdates(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

beforeEach(() => {
  vi.clearAllMocks()
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
  mockUpdateUpdates.mockResolvedValue({})
})

/** The flat shape api.ts's interceptor rejects with (api.ts:127-131). */
const serverFault = { status: 500, code: 'INTERNAL_ERROR', message: 'read failed' }

function makeSettings(overrides: Record<string, unknown> = {}) {
  return {
    scanIntervalMinutes: 720,
    lastScanAt: null,
    lastScanError: null,
    globalAutoUpdate: true,
    applyMode: 'immediate',
    applyTime: '03:00',
    applyDays: [0, 1, 2, 3, 4, 5, 6],
    autoUpdateStats: { enabledContainers: 3, updatesLast7Days: 2, updatesLast30Days: 9 },
    ...overrides,
  }
}

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <UpdateScheduleContent />
    </QueryClientProvider>,
  )
  return { ...view, queryClient }
}

describe('UpdateScheduleContent — a failed refresh is disclosed (agent-os-ptiq)', () => {
  /**
   * Resolve FIRST, then reject: a first-fetch-rejects fixture never puts a
   * populated form on screen for the refetch to strand, so it is blind to this
   * class — it lands in the `isError && !settings` branch that is already
   * correct.
   */
  it('keeps the populated form AND says the refresh failed', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings())
    const { queryClient } = renderPanel()

    expect(await screen.findByLabelText('Enable Auto-Update')).toBeChecked()

    mockGetUpdates.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(
        queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error'),
      ).toBe(true),
    )

    // Retention — already correct, pinned so the fix cannot "solve" the red by
    // throwing the form away. This half passes BEFORE the fix, which is what
    // makes the red below mean "retained AND undisclosed".
    expect(screen.getByLabelText('Enable Auto-Update')).toBeChecked()
    expect(screen.getByLabelText('Scan Interval')).toHaveTextContent('Every 12 hours')

    // Disclosure — ABSENT before the fix. beforeSave wording, because the
    // operator's next Save writes these fields back.
    expect(
      screen.getByText(
        /Could not refresh the update settings\. The values shown are the last ones the server sent, so check them before saving\./,
      ),
    ).toBeInTheDocument()
  })

  it('says nothing while the refresh is succeeding', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings())
    renderPanel()

    expect(await screen.findByLabelText('Enable Auto-Update')).toBeChecked()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('UpdateScheduleContent — counts the server did not vouch for (agent-os-xppj)', () => {
  /**
   * The backend omits autoUpdateStats when GetUpdateStats fails, so an absent
   * block means "this server is not asserting counts". Silently dropping the
   * section reads as "nothing to report", which is the same lie one layer up.
   */
  it('says the statistics are unavailable when the server sends no counts', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings({ autoUpdateStats: undefined }))
    renderPanel()

    expect(await screen.findByText('Update statistics are unavailable.')).toBeInTheDocument()
    // The counters must not be invented in their place.
    expect(screen.queryByText(/containers? with auto-update enabled/)).not.toBeInTheDocument()
  })

  /** CONTROL: counts that ARE vouched for still render, and say nothing extra. */
  it('renders the counts when the server sends them', async () => {
    mockGetUpdates.mockResolvedValue(makeSettings())
    renderPanel()

    expect(await screen.findByText('3 containers with auto-update enabled')).toBeInTheDocument()
    expect(
      screen.getByText('2 updates in the last 7 days, 9 in the last 30 days'),
    ).toBeInTheDocument()
    expect(screen.queryByText('Update statistics are unavailable.')).not.toBeInTheDocument()
  })
})
