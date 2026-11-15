/**
 * agent-os-npxh, the commit dialog's arm: the selected file's diff, fetched
 * while unlocked, holds secrets in the clear. After a lock it is not shown
 * while the lock's refetch is in flight, nor after that refetch fails.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'
import type { GitChangesResult } from '@/types/generated'

const mockChanges = vi.fn()
const mockChangesDiff = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    changes: (...a: unknown[]) => mockChanges(...a),
    changesDiff: (...a: unknown[]) => mockChangesDiff(...a),
    commit: vi.fn(),
    push: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { CommitPushDialog } from '../CommitPushDialog'

const SECRET = 'hunter2-plaintext'
const CHANGES: GitChangesResult = {
  branch: 'main',
  detached: false,
  trackingBranch: 'origin/main',
  hasUpstream: true,
  ahead: 0,
  behind: 0,
  pushEnabled: true,
  identityConfigured: true,
  changes: [{ path: 'compose.yaml', status: '.M', isEnv: false }],
}
const diffOf = (password: string) => ({
  path: 'compose.yaml',
  hidden: false,
  diff: `diff --git a/compose.yaml b/compose.yaml\n--- a/compose.yaml\n+++ b/compose.yaml\n@@ -1 +1 @@\n-      DB_PASSWORD: old\n+      DB_PASSWORD: ${password}\n`,
})

beforeEach(() => {
  vi.clearAllMocks()
  mockChangesDiff.mockReset()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
  mockChanges.mockResolvedValue(CHANGES)
})
afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('CommitPushDialog diff after a lock (agent-os-npxh)', () => {
  it.each([
    ['the lock refetch fails', () => Promise.reject(new Error('offline')), /Retry|Try again/],
    ['the lock refetch is still in flight', () => new Promise(() => {}), /Loading diff/],
  ])('%s: the unlocked diff is not on screen', async (_name, refetch, shown) => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    mockChangesDiff.mockResolvedValueOnce(diffOf(SECRET))
    const { queryClient } = renderWithProviders(
      <CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />,
    )
    await waitFor(() => expect(screen.getAllByText(SECRET, { exact: false }).length).toBeGreaterThan(0))

    mockChangesDiff.mockImplementation(refetch)
    act(() => useEnvUnlockStore.getState().lock())
    act(() => {
      void invalidateEnvUnlockQueries(queryClient)
    })
    await waitFor(() => expect(mockChangesDiff.mock.calls.length).toBeGreaterThanOrEqual(2))

    await waitFor(() => expect(screen.getAllByText(shown).length).toBeGreaterThan(0))
    expect(screen.queryAllByText(SECRET, { exact: false })).toHaveLength(0)
  })
})
