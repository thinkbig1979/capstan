import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { WSClient } from '@/lib/ws'
import { useAuthStore } from '@/stores/authStore'

class MockWebSocket {
  url: string
  readyState: number
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  static OPEN = 1
  static CLOSED = 3
  static CONNECTING = 0

  constructor(url: string) {
    this.url = url
    this.readyState = 0
    MockWebSocket.instance = this
  }

  send() {}
  close() {
    this.readyState = MockWebSocket.CLOSED
  }

  static instance: MockWebSocket | null = null
}

let originalWebSocket: typeof WebSocket

beforeEach(() => {
  originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  MockWebSocket.instance = null
  useAuthStore.setState({ authDisabled: true, token: null })
})

afterEach(() => {
  globalThis.WebSocket = originalWebSocket
})

describe('WSClient reconnect behavior', () => {
  it('does not call onReconnectFailed after intentional close', () => {
    const onReconnectFailed = vi.fn()
    const client = new WSClient()
    client.connect('/test', vi.fn(), { onReconnectFailed })

    MockWebSocket.instance!.onopen!()

    client.close()

    MockWebSocket.instance!.onclose!()

    expect(onReconnectFailed).not.toHaveBeenCalled()
  })

  it('does not call scheduleReconnect after intentional close', () => {
    const onReconnecting = vi.fn()
    const client = new WSClient()
    client.connect('/test', vi.fn(), { onReconnecting })

    MockWebSocket.instance!.onopen!()

    client.close()

    MockWebSocket.instance!.onclose!()

    expect(onReconnecting).not.toHaveBeenCalled()
  })

  it('does attempt reconnect after unexpected close', () => {
    const onReconnecting = vi.fn()
    const client = new WSClient()
    client.connect('/test', vi.fn(), { onReconnecting })

    MockWebSocket.instance!.onopen!()

    MockWebSocket.instance!.onclose!()

    expect(onReconnecting).toHaveBeenCalledWith(1)
  })
})
