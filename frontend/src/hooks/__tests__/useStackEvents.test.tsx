import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { toast } from 'sonner'
import { useUpdateScanStore } from '@/stores/updateScanStore'

// Capture the onMessage callback handed to the WS hook so tests can drive events.
let capturedOnMessage: ((data: unknown) => void) | null = null

vi.mock('../useWebSocket', () => ({
  useWebSocketJSON: (_path: string, onMessage: (d: unknown) => void) => {
    capturedOnMessage = onMessage
    return { lastMessage: null, status: 'open', send: vi.fn() }
  },
}))

vi.mock('@/lib/query-client', () => ({
  queryClient: { invalidateQueries: vi.fn(), setQueryData: vi.fn() },
}))

vi.mock('@/lib/api', () => ({
  resourcesApi: { checkUpdates: vi.fn() },
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: { loading: vi.fn(), success: vi.fn(), error: vi.fn(), dismiss: vi.fn() },
}))

import { parseStackEvent, useStackEvents } from '../useStackEvents'
import { UPDATE_SCAN_TOAST_ID } from '../useResources'
import { queryClient } from '@/lib/query-client'
import { queryKeys } from '@/lib/query-keys'
import type { UpdateJobOutcome } from '@/stores/updateJobStore'

// scheduleInvalidations debounces by 750ms, so every invalidation assertion has to
// let the timer fire first -- read before it does and the call list is still empty,
// which passes on the broken handler as readily as on the fixed one.
const flushInvalidations = () => {
  vi.advanceTimersByTime(750)
  return vi.mocked(queryClient.invalidateQueries).mock.calls.map(([arg]) => arg!.queryKey)
}

beforeEach(() => {
  capturedOnMessage = null
  useUpdateScanStore.setState({ isScanning: false })
  vi.clearAllMocks()
})

describe('useStackEvents update-scan completion', () => {
  it('finishes the scan and shows success on update_scan_complete', () => {
    useUpdateScanStore.setState({ isScanning: true })
    renderHook(() => useStackEvents())

    expect(capturedOnMessage).toBeTypeOf('function')
    capturedOnMessage!({ type: 'update_scan_complete', timestamp: '' })

    expect(useUpdateScanStore.getState().isScanning).toBe(false)
    expect(toast.success).toHaveBeenCalledWith('Update check complete', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 3000,
    })
  })

  it('finishes the scan and shows an error toast on update_scan_failed', () => {
    useUpdateScanStore.setState({ isScanning: true })
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_scan_failed', timestamp: '' })

    expect(useUpdateScanStore.getState().isScanning).toBe(false)
    expect(toast.error).toHaveBeenCalledWith('Update check failed', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 4000,
    })
  })

  it('does not toast when a background scan completes with no user-initiated scan', () => {
    // isScanning stays false — the user never clicked Check.
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_scan_complete', timestamp: '' })

    expect(toast.success).not.toHaveBeenCalled()
    expect(useUpdateScanStore.getState().isScanning).toBe(false)
  })
})

describe('useStackEvents update-scan invalidation', () => {
  // scheduleInvalidations debounces by 750ms, so the assertions run on fake timers.
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('invalidates the update-settings query on update_scan_failed, so lastScanError is refetched', () => {
    useUpdateScanStore.setState({ isScanning: true })
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_scan_failed', timestamp: '' })

    expect(flushInvalidations()).toEqual([
      queryKeys.resources.updates(),
      queryKeys.settings.updates(),
    ])
  })

  it('still invalidates both queries on update_scan_complete', () => {
    useUpdateScanStore.setState({ isScanning: true })
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_scan_complete', timestamp: '' })

    expect(flushInvalidations()).toEqual([
      queryKeys.resources.updates(),
      queryKeys.settings.updates(),
    ])
  })
})

describe('useStackEvents update-completion invalidation', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  const jobCompleteEvent = (outcome: UpdateJobOutcome) => ({
    type: 'update_job_complete',
    jobId: 'job-1',
    targetType: 'container',
    targetId: 'ctr-1',
    stackId: 'stack-1',
    name: 'web',
    status: outcome === 'success' ? 'success' : 'error',
    outcome,
  })

  it('invalidates the update-settings query on update_completed, so the 7/30-day stats refetch', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_completed', timestamp: '' })

    expect(flushInvalidations()).toEqual([
      queryKeys.updateHistory.all(),
      queryKeys.settings.updates(),
      queryKeys.resources.updates(),
      queryKeys.dashboardStats(),
      queryKeys.stacks(),
    ])
  })

  it('invalidates the update-settings query on a successful update_job_complete', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!(jobCompleteEvent('success'))

    expect(flushInvalidations()).toEqual([
      queryKeys.updateHistory.all(),
      queryKeys.settings.updates(),
      queryKeys.dashboardStats(),
      queryKeys.stacks(),
      queryKeys.resources.updates(),
    ])
  })

  // agent-os-mgly: the handler once branched on the outcome and did the same in
  // both arms. This pins the whole flushed SET per outcome, so collapsing the
  // branch (or ever making it real) cannot change what is invalidated unseen.
  // 'unknown' is an outcome string the type does not name.
  it.each(['success', 'no_change', 'failed', 'unknown'])(
    'invalidates the identical key set on update_job_complete with outcome %s',
    (outcome) => {
      renderHook(() => useStackEvents())

      capturedOnMessage!(jobCompleteEvent(outcome as UpdateJobOutcome))

      expect(flushInvalidations()).toEqual([
        queryKeys.updateHistory.all(),
        queryKeys.settings.updates(),
        queryKeys.dashboardStats(),
        queryKeys.stacks(),
        queryKeys.resources.updates(),
      ])
    },
  )

  it('invalidates the update-settings query on a failed update_job_complete too', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!(jobCompleteEvent('failed'))

    expect(flushInvalidations()).toEqual([
      queryKeys.updateHistory.all(),
      queryKeys.settings.updates(),
      queryKeys.dashboardStats(),
      queryKeys.stacks(),
      queryKeys.resources.updates(),
    ])
  })
})

// agent-os-4ato: a stack_status frame named one container's action, and its
// status was written into the stacks-list row, so one dead service of three read
// "stopped" (truth: partial). The backend no longer sends one; a frame that
// still arrives refetches the list instead, so the row only ever shows the
// status the server derived from all the project's containers. That refetch is
// also what clears statusStale (agent-os-xjzr): the list is re-read live.
describe('useStackEvents stack_status refetches instead of writing the row', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('never writes the frame status into the list and refetches the list, the stack and the stats', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'stack_status', stackId: 's1', status: 'stopped', timestamp: '' })

    expect(vi.mocked(queryClient.setQueryData)).not.toHaveBeenCalled()
    const keys = flushInvalidations().map((k) => JSON.stringify(k))
    expect(keys).toEqual(expect.arrayContaining([
      JSON.stringify(queryKeys.stacks()),
      JSON.stringify(queryKeys.stack.detail('s1')),
      JSON.stringify(queryKeys.dashboardStats()),
    ]))
  })
})

// agent-os-4ato round 2 (review F2): every container_event now carries stack
// status convergence through scheduleInvalidations, which was trailing-only:
// each event restarted one 750 ms timer, so a host emitting an event every
// 500 ms never refetched. A max wait flushes at most 2 s after the first
// pending key.
describe('useStackEvents invalidation debounce has a max wait', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('still refetches the stack list at least every 2 s under a steady event stream', () => {
    const start = Date.now()
    const flushes: number[] = []
    vi.mocked(queryClient.invalidateQueries).mockImplementation((arg) => {
      if (JSON.stringify(arg?.queryKey) === JSON.stringify(queryKeys.stacks())) flushes.push(Date.now() - start)
      return Promise.resolve()
    })
    renderHook(() => useStackEvents())

    for (let t = 0; t < 30_000; t += 500) {
      capturedOnMessage!({ type: 'container_event', stackId: 's1', containerId: 'c' + t, event: 'die', timestamp: '' })
      vi.advanceTimersByTime(500)
    }

    expect(flushes.length).toBeGreaterThanOrEqual(14)
    expect(flushes[0]).toBeLessThanOrEqual(2000)
    for (let i = 1; i < flushes.length; i++) {
      expect(flushes[i] - flushes[i - 1]).toBeLessThanOrEqual(2000)
    }
  })

  it('still waits for a quiet gap when events are sparse', () => {
    renderHook(() => useStackEvents())
    capturedOnMessage!({ type: 'container_event', stackId: 's1', containerId: 'c1', event: 'die', timestamp: '' })
    vi.advanceTimersByTime(749)
    expect(vi.mocked(queryClient.invalidateQueries)).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(vi.mocked(queryClient.invalidateQueries)).toHaveBeenCalled()
  })
})

// agent-os-a1ye.5: the backend re-subscribes to a lost Docker event stream and
// then sends events_resync, because events from the gap are gone. The frame
// must survive validation and refetch everything a missed container event
// would have touched; dropping it leaves stack statuses frozen until focus.
describe('useStackEvents events_resync', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('accepts the frame instead of dropping it as unknown', () => {
    expect(parseStackEvent({ type: 'events_resync', status: '', targetType: '', timestamp: 't' }))
      .toEqual({ type: 'events_resync', timestamp: 't' })
  })

  it('refetches the stack list and dashboard stats', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'events_resync', timestamp: '' })

    expect(flushInvalidations()).toEqual([
      queryKeys.stacks(),
      queryKeys.dashboardStats(),
    ])
  })
})

// agent-os-8y80: a prune, delete or free-up broadcasts resource_changed, and the
// Docker host summary (page, and later badge/pill/tiles) must refetch with the
// lists rather than wait for its 60 s poll.
describe('useStackEvents resource_changed', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('refetches the host summary with the resource lists', () => {
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'resource_changed', event: 'prune', timestamp: '' })

    expect(flushInvalidations()).toEqual([
      queryKeys.resources.images(),
      queryKeys.resources.volumes(),
      queryKeys.resources.networks(),
      queryKeys.resources.buildCache(),
      queryKeys.resources.hostSummary(),
      queryKeys.dashboardStats(),
    ])
  })
})
