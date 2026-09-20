/**
 * agent-os-6iui. agent-os-wczm re-keyed `toGlobalAutoUpdateState` on
 * `isError && !query.data`, so a failed REFETCH keeps reporting the state the
 * server already sent instead of blanking every toggle to 'unavailable'. That
 * half is right. What it removed and did not replace is the DISCLOSURE: before
 * the change an error locked the toggle and labelled it, and after it the
 * control is live and unlabelled over possibly-stale state — on a WRITE
 * surface.
 *
 * Kept out of ContainersOverviewTab.test.tsx only in the sense of scope, not
 * fixture: that file's fixture would work. This one is separate so the
 * disclosure contract reads as one story across its three consumers
 * (DashboardPageAutoUpdateRefresh.test.tsx,
 * StackDetailAutoUpdateRefresh.test.tsx).
 *
 * WHY THE ARM IS SHAPED THIS WAY: `AutoUpdateToggle.test.tsx` covers only the
 * pure function, one layer BELOW where the defect now lives, which is exactly
 * why this gap shipped green. And a first-fetch-rejects fixture is blind to the
 * class — it never puts a known state on screen for the refetch to strand.
 * Resolve first, THEN reject.
 *
 * CAVEAT, stated rather than overclaimed: mocking `@/lib/api` bypasses the axios
 * response interceptor that builds `{ ...error.response.data, status }`
 * (api.ts:127-131). A green here proves the component HANDLES that shape; it
 * does not prove the wire PRODUCES it.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/utils'
import { ContainersOverviewTab } from '../ContainersOverviewTab'
import type { DashboardStats, DashboardContainerInfo } from '@/types'

const { stacksMock, autoUpdateMock } = vi.hoisted(() => ({
  stacksMock: { list: vi.fn() },
  autoUpdateMock: { getPolicies: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn(), info: vi.fn(), warning: vi.fn() },
  Toaster: () => null,
}))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    stacksApi: { ...actual.stacksApi, ...stacksMock },
    autoUpdateApi: { ...actual.autoUpdateApi, ...autoUpdateMock },
  }
})

/** The flat shape api.ts's interceptor rejects with (api.ts:127-131). */
const serverFault = { status: 500, code: 'POLICY_READ_FAILED', message: 'policy read failed' }

const policies = {
  policies: [
    { targetType: 'container', targetId: 'c1', enabled: true, paused: false, consecutiveFailures: 0 },
  ],
  globalEnabled: true,
}

function makeContainer(overrides: Partial<DashboardContainerInfo> = {}): DashboardContainerInfo {
  return {
    id: 'c1',
    name: 'web',
    image: 'nginx:latest',
    state: 'running',
    status: 'Up 2 hours',
    health: '',
    ports: [],
    stackId: 'stack-1',
    stackLookupFailed: false,
    projectName: 'myproject',
    restartCount: 0,
    created: '2026-01-01T00:00:00Z',
    startedAt: '2026-01-01T00:00:00Z',
    diskSize: 0,
    imageSize: 0,
    ...overrides,
  } as DashboardContainerInfo
}

/**
 * Both tabs get a row. `isStandaloneContainer` (agent-os-yrgn) sends a row to
 * the standalone bucket when EITHER projectName or stackId is missing, so c2
 * lands in "Other Containers" — which is what gives the persistence assertion
 * below something to discriminate against.
 */
function renderTab() {
  const stats = {
    containers: [makeContainer(), makeContainer({ id: 'c2', name: 'sidecar', stackId: '' })],
  } as unknown as DashboardStats
  return renderWithProviders(
    <ContainersOverviewTab stats={stats} latestMetrics={{}} metricsStatus="connected" />,
  )
}

describe('ContainersOverviewTab — a failed auto-update refetch is disclosed (agent-os-6iui)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    stacksMock.list.mockResolvedValue([])
    autoUpdateMock.getPolicies.mockResolvedValue(policies)
  })

  /**
   * THE fail-first arm. Two assertions, and both are load-bearing:
   *
   *  - the toggle is STILL LIVE on its last-known state. An arm that only
   *    demanded the notice would go red for a build that locked the toggle
   *    again, which is the thing agent-os-wczm deliberately stopped doing.
   *  - the failure is DISCLOSED. This is the half that is absent before the fix.
   *
   * The `status === 'error'` wait is not decoration: `refetchQueries()` can
   * return before React re-renders, and without it the arm can pass on a render
   * that never saw the failure at all.
   */
  it('keeps the toggles live on their last-known state AND says the refresh failed', async () => {
    const { queryClient } = renderTab()

    // Unlocked and checked: the state the server really sent.
    expect(await screen.findByLabelText('Auto-update container c1')).toBeChecked()

    autoUpdateMock.getPolicies.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(
        queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error'),
      ).toBe(true),
    )

    // Retention (already correct, pinned so the fix cannot regress it).
    expect(screen.getByLabelText('Auto-update container c1')).toBeChecked()
    expect(
      screen.queryByLabelText(/Auto-update container c1 \(locked/),
    ).not.toBeInTheDocument()

    // Disclosure — ABSENT before the fix.
    expect(
      screen.getByText(
        /Could not refresh the auto-update settings\. The values shown are the last ones the server sent\./,
      ),
    ).toBeInTheDocument()
  })

  /**
   * The granularity decision, pinned — and pinned by something that can
   * actually tell the two candidate implementations apart.
   *
   * A COUNT CANNOT. `ContainerTable` appears twice in the source, but the two
   * sites sit in different Radix `TabsContent` panels and Radix unmounts the
   * inactive one, so at most ONE is mounted. `getAllByRole('alert')` therefore
   * returns 1 for the LIFTED placement AND for the rejected IN-TABLE one. An
   * assertion that passes for both decides nothing.
   *
   * Predicted, before running, for each candidate:
   *
   *   POSITION      lifted:   1 alert, and the open tabpanel contains none.
   *                 in-table: 1 alert, and it IS inside the open tabpanel.
   *   PERSISTENCE   lifted:   the SAME DOM node survives a tab switch.
   *                 in-table: that node unmounts with its panel; a different
   *                           one mounts inside the newly opened panel.
   *
   * The predictions differ on both, so the arm discriminates. Verified by
   * mutation, not just by prediction: with the notice moved inside
   * ContainerTable this test fails on the position assertion.
   */
  it('renders the notice above both tab panels, not inside the open one', async () => {
    const { queryClient } = renderTab()
    expect(await screen.findByLabelText('Auto-update container c1')).toBeChecked()

    autoUpdateMock.getPolicies.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(
        queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error'),
      ).toBe(true),
    )

    // POSITION
    const [notice] = screen.getAllByRole('alert')
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(within(screen.getByRole('tabpanel')).queryByRole('alert')).not.toBeInTheDocument()

    // PERSISTENCE. Both tabs hold a ContainerTable in this fixture, so the
    // other panel is not merely empty — an in-table notice would really
    // re-mount over there rather than vanishing.
    await userEvent.click(screen.getByRole('tab', { name: /Other Containers/ }))
    expect(await screen.findByLabelText('Auto-update container c2')).toBeInTheDocument()
    expect(notice).toBeInTheDocument()
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(within(screen.getByRole('tabpanel')).queryByRole('alert')).not.toBeInTheDocument()
  })

  /** The negative arm: no failure, no notice. Without it the arm above passes
   *  against a build that renders the notice unconditionally. */
  it('says nothing while the refresh is succeeding', async () => {
    renderTab()
    expect(await screen.findByLabelText('Auto-update container c1')).toBeChecked()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
