/**
 * agent-os-6iui, the StackDetail consumer. See
 * dashboard/__tests__/ContainersOverviewTabAutoUpdateRefresh.test.tsx for the
 * class: agent-os-wczm kept the last-known toggle state through a failed
 * refetch and removed the disclosure that used to come with locking it.
 *
 * Separate from StackDetail.test.tsx because that file STUBS AutoUpdateToggle
 * (legitimately — it is testing tab wiring, and the toggle has its own direct
 * coverage). A stubbed toggle cannot show whether the state on screen is the
 * retained one, which is half of what this arm has to discriminate.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { StackDetail } from '../StackDetail'
import type { Stack } from '@/types'

const mockGetPolicies = vi.fn()

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    autoUpdateApi: { ...actual.autoUpdateApi, getPolicies: (...a: unknown[]) => mockGetPolicies(...a) },
  }
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// jsdom has no WebSocket; the Overview grid's live-metrics stream is not under
// test here (useMetricsBase has its own direct test).
vi.mock('@/hooks/useMetricsBase', () => ({
  useMetricsBase: () => ({
    containers: [],
    baseAggregates: { totalCpuPercent: 0, totalMemUsage: 0, totalMemLimit: 0, totalMemPercent: 0 },
    latestMetrics: {},
    isConnected: false,
    ws: {},
  }),
}))

// Stubbed because each has its own direct test and neither is what this file
// discriminates. AutoUpdateToggle is deliberately NOT stubbed.
vi.mock('../ContainerList', () => ({
  ContainerList: () => <div data-testid="container-list" />,
}))
vi.mock('@/components/dashboard/BackupToggle', () => ({
  BackupToggle: () => <div data-testid="backup-toggle" />,
}))

/** The flat shape api.ts's interceptor rejects with (api.ts:127-131). */
const serverFault = { status: 500, code: 'POLICY_READ_FAILED', message: 'policy read failed' }

const stack = {
  id: 'stack-1',
  name: 'myapp',
  status: 'running',
  composeFile: 'docker-compose.yaml',
  envFile: '.env',
  containers: [{ id: 'c1', name: 'web' }],
} as unknown as Stack

const policies = {
  policies: [
    { targetType: 'stack', targetId: 'stack-1', enabled: true, paused: false, consecutiveFailures: 0 },
  ],
  globalEnabled: true,
}

describe('StackDetail overview — a failed auto-update refetch is disclosed (agent-os-6iui)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetPolicies.mockResolvedValue(policies)
  })

  /** Resolve first, THEN reject: a first-fetch-rejects fixture never puts a
   *  known state on screen for the refetch to strand, so it is blind here. */
  it('keeps the toggle live on its last-known state AND says the refresh failed', async () => {
    const { queryClient } = renderWithProviders(
      <StackDetail stack={stack} activeTab="overview" onTabChange={() => {}} />,
    )

    expect(await screen.findByLabelText('Auto-update stack stack-1')).toBeChecked()

    mockGetPolicies.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(
        queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error'),
      ).toBe(true),
    )

    // Retention (already correct, pinned so the fix cannot regress it).
    expect(screen.getByLabelText('Auto-update stack stack-1')).toBeChecked()
    expect(screen.queryByLabelText(/Auto-update stack stack-1 \(locked/)).not.toBeInTheDocument()

    // Disclosure — ABSENT before the fix.
    expect(
      screen.getByText(
        /Could not refresh the auto-update settings\. The values shown are the last ones the server sent\./,
      ),
    ).toBeInTheDocument()
  })

  it('says nothing while the refresh is succeeding', async () => {
    renderWithProviders(<StackDetail stack={stack} activeTab="overview" onTabChange={() => {}} />)
    expect(await screen.findByLabelText('Auto-update stack stack-1')).toBeChecked()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
