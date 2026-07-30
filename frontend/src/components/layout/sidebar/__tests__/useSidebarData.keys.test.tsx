import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockGetStatus = vi.fn()

vi.mock('@/lib/api', () => ({
  backupApi: {
    getStatus: (...args: unknown[]) => mockGetStatus(...args),
  },
  resourcesApi: { checkUpdates: vi.fn().mockResolvedValue({ updates: [] }) },
  settingsApi: { getConfig: vi.fn().mockResolvedValue({ stacksDirectories: [] }) },
  stacksApi: { list: vi.fn().mockResolvedValue([]) },
}))

import { useSidebarData } from '../useSidebarData'
import { queryKeys } from '@/lib/query-keys'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return { wrapper, queryClient }
}

const params = {
  searchQuery: '',
  statusFilter: 'all' as const,
  sortBy: 'name' as const,
  pinnedStacks: [],
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetStatus.mockResolvedValue({ lastRun: null, nextRun: null })
})

describe('useSidebarData backup-status cache key', () => {
  // Regression: the sidebar footer fetched backup status under a bespoke
  // ['backup-status'] key while every writer (BackupStatusCard, BackupsTab,
  // useBackup mutations) invalidated the canonical ['backup','status']. The two
  // never intersected, so the footer went stale until its 60s refetchInterval
  // happened to fire. Both sides must resolve through the same factory entry.
  it('refetches when the canonical backup status key is invalidated', async () => {
    const { wrapper, queryClient } = createWrapper()
    renderHook(() => useSidebarData(params), { wrapper })

    await waitFor(() => expect(mockGetStatus).toHaveBeenCalledTimes(1))

    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.backup.status() })
    })

    await waitFor(() => expect(mockGetStatus).toHaveBeenCalledTimes(2))
  })

  it('registers the footer query under the canonical backup status key', async () => {
    const { wrapper, queryClient } = createWrapper()
    renderHook(() => useSidebarData(params), { wrapper })

    await waitFor(() => expect(mockGetStatus).toHaveBeenCalledTimes(1))

    const keys = queryClient.getQueryCache().getAll().map((q) => q.queryKey)
    expect(keys).toContainEqual([...queryKeys.backup.status()])
    expect(keys).not.toContainEqual(['backup-status'])
  })
})
