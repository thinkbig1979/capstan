/**
 * agent-os-6iui, the DashboardPage consumer. See
 * components/dashboard/__tests__/ContainersOverviewTabAutoUpdateRefresh.test.tsx
 * for the class: agent-os-wczm kept the last-known toggle state through a
 * failed refetch and removed the disclosure that used to come with locking it.
 *
 * Separate from DashboardPage.test.tsx because that file STUBS StacksTab and
 * only surfaces `globalAutoUpdateState` as text. That fixture can see the
 * derived state, but not whether the per-stack toggles it feeds are still live
 * — and "still live, undisclosed" is the defect.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { DashboardPage } from '../DashboardPage'
import { queryKeys } from '@/lib/query-keys'
import type { Stack } from '@/types'

const {
  listStacks, listDirectories, scanDirectories, dashboardStats, getConfig, getPolicies,
} = vi.hoisted(() => ({
  listStacks: vi.fn(),
  listDirectories: vi.fn(),
  scanDirectories: vi.fn(),
  dashboardStats: vi.fn(),
  getConfig: vi.fn(),
  getPolicies: vi.fn(),
}))

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    stacksApi: { ...actual.stacksApi, list: listStacks },
    directoriesApi: { ...actual.directoriesApi, list: listDirectories, scan: scanDirectories },
    dashboardApi: { ...actual.dashboardApi, stats: dashboardStats },
    settingsApi: { ...actual.settingsApi, getConfig },
    autoUpdateApi: { ...actual.autoUpdateApi, getPolicies },
  }
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// useDashboardMetrics opens a real WebSocket; there is no WS shim in
// test/setup.ts and the hook has its own dedicated coverage.
vi.mock('@/hooks/DashboardMetricsContext', () => ({
  useDashboardMetricsContext: () => ({
    containers: [],
    aggregates: {},
    latestMetrics: {},
    isConnected: false,
    ws: { status: 'closed' },
  }),
}))

// Stubbed because each has its own direct test and none is what this file
// discriminates. StacksTab and AutoUpdateToggle are deliberately NOT stubbed.
vi.mock('@/components/dashboard/DashboardHeader', () => ({
  DashboardHeader: () => <div data-testid="dashboard-header" />,
}))
vi.mock('@/components/dashboard/AttentionStrip', () => ({
  AttentionStrip: () => <div data-testid="attention-strip" />,
}))
vi.mock('@/components/dashboard/HostStrip', () => ({
  HostStrip: () => <div data-testid="host-strip" />,
}))
vi.mock('@/components/dashboard/BackupToggle', () => ({
  BackupToggle: () => <div data-testid="backup-toggle" />,
}))

/** The flat shape api.ts's interceptor rejects with (api.ts:127-131). */
const serverFault = { status: 500, code: 'POLICY_READ_FAILED', message: 'policy read failed' }

const stacks = [
  { id: 'stack-1', name: 'myapp', status: 'running', directory: '/srv/myapp', containers: [] },
] as unknown as Stack[]

const policies = {
  policies: [
    { targetType: 'stack', targetId: 'stack-1', enabled: true, paused: false, consecutiveFailures: 0 },
  ],
  globalEnabled: true,
}

describe('DashboardPage stacks tab — a failed auto-update refetch is disclosed (agent-os-6iui)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listStacks.mockResolvedValue(stacks)
    listDirectories.mockResolvedValue([])
    scanDirectories.mockResolvedValue([])
    dashboardStats.mockResolvedValue({ containers: [] })
    getConfig.mockResolvedValue({})
    getPolicies.mockResolvedValue(policies)
  })

  /** Resolve first, THEN reject: a first-fetch-rejects fixture never puts a
   *  known state on screen for the refetch to strand, so it is blind here. */
  it('keeps the toggle live on its last-known state AND says the refresh failed', async () => {
    const { queryClient } = renderWithProviders(<DashboardPage />)

    expect(await screen.findByLabelText('Auto-update stack stack-1')).toBeChecked()

    getPolicies.mockRejectedValue(serverFault)
    await queryClient.refetchQueries({ queryKey: queryKeys.autoUpdatePolicies() })
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
    renderWithProviders(<DashboardPage />)
    expect(await screen.findByLabelText('Auto-update stack stack-1')).toBeChecked()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
