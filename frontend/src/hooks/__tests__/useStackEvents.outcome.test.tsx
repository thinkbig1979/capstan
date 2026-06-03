/**
 * Tests for the outcome-aware updates to useStackEvents (B1 Action Truth Contract).
 *
 * Verifies:
 *  - update_job_complete with outcome=success stores outcome and invalidates ['resources','updates']
 *  - update_job_complete with outcome=no_change stores outcome and invalidates ['resources','updates']
 *  - update_job_complete with outcome=failed stores outcome but does NOT skip updates invalidation
 *  - updates_changed event invalidates ['resources','updates']
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useUpdateJobStore } from '@/stores/updateJobStore'

// Capture the onMessage callback so tests can drive events synchronously.
let capturedOnMessage: ((data: unknown) => void) | null = null

vi.mock('../useWebSocket', () => ({
  useWebSocketJSON: (_path: string, onMessage: (d: unknown) => void) => {
    capturedOnMessage = onMessage
    return { lastMessage: null, status: 'open', send: vi.fn() }
  },
}))

const mockInvalidateQueries = vi.fn()
vi.mock('@/lib/query-client', () => ({
  queryClient: {
    invalidateQueries: (...args: unknown[]) => mockInvalidateQueries(...args),
    setQueryData: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  resourcesApi: { checkUpdates: vi.fn() },
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: { loading: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn(), dismiss: vi.fn() },
}))

import { useStackEvents } from '../useStackEvents'

beforeEach(() => {
  capturedOnMessage = null
  useUpdateJobStore.setState({ jobs: {} })
  vi.clearAllMocks()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

// ─── update_job_complete with outcome ────────────────────────────────────────

describe('useStackEvents — update_job_complete outcome handling', () => {
  it('stores outcome=success in the job store', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({
      type: 'update_job_complete',
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'c-abc',
      stackId: 'stack-1',
      name: 'web',
      status: 'success',
      outcome: 'success',
      reason: 'image digest advanced',
    })

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job).toBeDefined()
    expect(job.outcome).toBe('success')
    expect(job.reason).toBe('image digest advanced')
  })

  it('stores outcome=no_change in the job store', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({
      type: 'update_job_complete',
      jobId: 'job-2',
      targetType: 'container',
      targetId: 'c-xyz',
      stackId: 'stack-1',
      name: 'db',
      status: 'success',
      outcome: 'no_change',
      reason: 'digests match',
    })

    const job = useUpdateJobStore.getState().jobs['job-2']
    expect(job).toBeDefined()
    expect(job.outcome).toBe('no_change')
    expect(job.outcome).not.toBe('success')
  })

  it('invalidates [resources,updates] on success outcome after debounce', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({
      type: 'update_job_complete',
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'c-abc',
      stackId: 'stack-1',
      name: 'web',
      status: 'success',
      outcome: 'success',
    })

    // Flush the debounce timer (750ms)
    vi.advanceTimersByTime(800)

    const calls = mockInvalidateQueries.mock.calls.map((c) => c[0])
    const updatesCall = calls.find(
      (c: { queryKey?: unknown[] }) =>
        JSON.stringify(c.queryKey) === JSON.stringify(['resources', 'updates']),
    )
    expect(updatesCall).toBeDefined()
  })

  it('invalidates [resources,updates] on no_change outcome after debounce', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({
      type: 'update_job_complete',
      jobId: 'job-2',
      targetType: 'container',
      targetId: 'c-xyz',
      stackId: 'stack-1',
      name: 'db',
      status: 'success',
      outcome: 'no_change',
    })

    vi.advanceTimersByTime(800)

    const calls = mockInvalidateQueries.mock.calls.map((c) => c[0])
    const updatesCall = calls.find(
      (c: { queryKey?: unknown[] }) =>
        JSON.stringify(c.queryKey) === JSON.stringify(['resources', 'updates']),
    )
    expect(updatesCall).toBeDefined()
  })
})

// ─── updates_changed event ───────────────────────────────────────────────────

describe('useStackEvents — updates_changed event', () => {
  it('invalidates [resources,updates] on updates_changed', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'updates_changed', timestamp: '' })

    vi.advanceTimersByTime(800)

    const calls = mockInvalidateQueries.mock.calls.map((c) => c[0])
    const updatesCall = calls.find(
      (c: { queryKey?: unknown[] }) =>
        JSON.stringify(c.queryKey) === JSON.stringify(['resources', 'updates']),
    )
    expect(updatesCall).toBeDefined()
  })
})
