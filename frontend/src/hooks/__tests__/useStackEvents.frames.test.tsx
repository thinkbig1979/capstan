import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useAuthStore } from '@/stores/authStore'

vi.mock('@/lib/query-client', () => ({
  queryClient: { invalidateQueries: vi.fn(), setQueryData: vi.fn() },
}))

import { useStackEvents } from '../useStackEvents'
import { queryClient } from '@/lib/query-client'
import { queryKeys } from '@/lib/query-keys'

/**
 * agent-os-r4kf. The sibling useStackEvents tests mock useWebSocketJSON and
 * call the handler directly, so none of them passes through parseStackEvent.
 * This file fakes the global WebSocket instead, so every frame takes the real
 * route: JSON text -> hook -> validator -> handler.
 */

class MockWebSocket {
  static instance: MockWebSocket | null = null
  url: string
  readyState = 0
  onopen: (() => void) | null = null
  onclose: ((e?: unknown) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e: Event) => void) | null = null

  constructor(url: string) {
    this.url = url
    MockWebSocket.instance = this
  }

  send() {}
  close() {
    this.readyState = 3
  }
}

let originalWebSocket: typeof WebSocket

const deliver = (payload: unknown) =>
  act(() => {
    MockWebSocket.instance!.onmessage!({ data: JSON.stringify(payload) })
  })

const flushInvalidations = () => {
  act(() => {
    vi.advanceTimersByTime(750)
  })
  return vi.mocked(queryClient.invalidateQueries).mock.calls.map(([arg]) => arg!.queryKey)
}

beforeEach(() => {
  originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  MockWebSocket.instance = null
  useAuthStore.setState({
    token: null,
    user: null,
    isAuthenticated: false,
    authDisabled: true,
    needsSetup: false,
  })
  vi.clearAllMocks()
})

afterEach(() => {
  vi.useRealTimers()
  globalThis.WebSocket = originalWebSocket
})

const mount = async () => {
  renderHook(() => useStackEvents())
  await waitFor(() => expect(MockWebSocket.instance).not.toBeNull())
  vi.useFakeTimers()
}

describe('useStackEvents through the real frame validator', () => {
  it('delivers a container_event with no stackId, and the stackId guard skips the stack keys', async () => {
    await mount()

    // monitor.go unassociatedStackEvent: StackID "" is omitted by encoding/json.
    deliver({ type: 'container_event', containerId: 'c1', event: 'die', timestamp: '2026-09-23T10:00:00Z' })

    const keys = flushInvalidations()
    // It reached the handler: dashboardStats is the key it always schedules.
    expect(keys).toContainEqual(queryKeys.dashboardStats())
    // ...and `if (event.stackId)` skipped the stack keys, since "" is falsy.
    expect(keys).not.toContainEqual(queryKeys.stacks())
  })

  it('schedules the stack keys for a container_event that does carry a stackId', async () => {
    await mount()

    deliver({ type: 'container_event', stackId: 's1', containerId: 'c1', event: 'die', timestamp: '2026-09-23T10:00:00Z' })

    const keys = flushInvalidations()
    expect(keys).toContainEqual(queryKeys.stacks())
    expect(keys).toContainEqual(queryKeys.stack.detail('s1'))
  })

  it('drops a container_event whose containerId is a number, so nothing is scheduled', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await mount()

    deliver({ type: 'container_event', containerId: 7, event: 'die', timestamp: '2026-09-23T10:00:00Z' })

    expect(flushInvalidations()).toEqual([])
    warn.mockRestore()
  })
})
