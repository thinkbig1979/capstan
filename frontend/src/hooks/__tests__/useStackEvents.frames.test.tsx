import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useAuthStore } from '@/stores/authStore'

vi.mock('@/lib/query-client', () => ({
  queryClient: { invalidateQueries: vi.fn(), setQueryData: vi.fn() },
}))

import { useStackEvents } from '../useStackEvents'
import { queryClient } from '@/lib/query-client'
import { queryKeys } from '@/lib/query-keys'
import type { Stack } from '@/types'

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

  // agent-os-n97z. Behaviour here is unchanged by the fix (the old code wrote
  // 'paused' too, through an `as StackStatus` cast), so this pins the cache
  // contract rather than failing first; the fail-first arms are the render
  // tests in StatusBadge/StackRow/StacksTab/Sidebar/DashboardPage.
  it('writes a paused stack_status into the stacks cache as paused', async () => {
    await mount()

    deliver({ type: 'stack_status', stackId: 's1', containerId: 'c1', event: 'pause', status: 'paused', timestamp: '2026-09-23T10:00:00Z' })

    const calls = vi.mocked(queryClient.setQueryData).mock.calls
    expect(calls).toHaveLength(1)
    const [key, updater] = calls[0] as unknown as [unknown, (old: Stack[] | undefined) => Stack[] | undefined]
    expect(key).toEqual(queryKeys.stacks())
    const old = [{ id: 's1', status: 'running' }, { id: 's2', status: 'running' }] as Stack[]
    expect(updater(old)?.map((s) => [s.id, s.status])).toEqual([['s1', 'paused'], ['s2', 'running']])
  })

  // agent-os-oomh: handlers/backup.go upsertPolicy broadcasts this after saving a
  // backup policy. It used to be dropped by the validator as an unknown type.
  it('invalidates the backup-policy queries on backup_policy_changed', async () => {
    await mount()

    deliver({ type: 'backup_policy_changed', timestamp: '2026-09-23T10:00:00Z' })

    const keys = flushInvalidations()
    expect(keys).toContainEqual(queryKeys.backup.policies())
    expect(keys).toContainEqual(queryKeys.backup.status())
  })
})
