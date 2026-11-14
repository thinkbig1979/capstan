/**
 * agent-os-lgzv: once the backup stream's redial ladder is spent, the client
 * stays closed. Before, useBackupStreaming kept the WSClient but left its
 * online/visibilitychange listeners attached (lib/ws.ts), so every tab focus
 * started another ladder of failing dials until the page was left.
 *
 * Driven through the real WSClient; only the browser WebSocket is faked.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useAuthStore } from '@/stores/authStore'
import { useBackupStreaming } from '../useBackup'

class FakeWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  static instances: FakeWebSocket[] = []
  readyState = FakeWebSocket.CONNECTING
  binaryType = 'blob'
  onopen: (() => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  closeCalls = 0
  url: string
  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }
  close() {
    this.closeCalls++
    this.readyState = FakeWebSocket.CLOSED
  }
  /** The server going away: a close the client did not ask for. */
  drop() {
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.({ code: 1006 })
  }
}

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>
}

/** Drops every socket until the ladder gives up; returns the dial count. */
function exhaustLadder(): number {
  for (let i = 0; i < 20; i++) {
    const last = FakeWebSocket.instances.at(-1)!
    if (last.readyState === FakeWebSocket.CLOSED) break
    act(() => { last.drop() })
    act(() => { vi.runOnlyPendingTimers() })
  }
  return FakeWebSocket.instances.length
}

beforeEach(() => {
  vi.useFakeTimers()
  FakeWebSocket.instances = []
  vi.stubGlobal('WebSocket', FakeWebSocket)
  useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('agent-os-lgzv: a backup stream that failed to reconnect stays closed', () => {
  it('focus and online after the ladder is spent dial nothing', () => {
    const { result, unmount } = renderHook(() => useBackupStreaming(), { wrapper })
    act(() => { result.current.connect('/ws/backups/run/abc') })

    const dials = exhaustLadder()
    // Fixture: the first dial plus the five redials, then the ladder gave up.
    expect(dials).toBe(6)
    expect(result.current.error).toBe('Connection lost, reconnect failed')

    act(() => {
      document.dispatchEvent(new Event('visibilitychange'))
      window.dispatchEvent(new Event('online'))
    })
    act(() => { vi.runOnlyPendingTimers() })

    expect(FakeWebSocket.instances.length).toBe(dials)
    unmount()
  })

  // Control on the same instrument: a live stream is closed by unmount, and
  // a focus after that dials nothing either.
  it('a live stream is closed on unmount', () => {
    const { result, unmount } = renderHook(() => useBackupStreaming(), { wrapper })
    act(() => { result.current.connect('/ws/backups/run/abc') })
    const live = FakeWebSocket.instances[0]

    unmount()

    expect(live.closeCalls).toBe(1)
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    expect(FakeWebSocket.instances.length).toBe(1)
  })
})
