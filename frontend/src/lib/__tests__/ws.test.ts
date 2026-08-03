import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { WSClient, MAX_RECONNECT_DELAY_MS } from '@/lib/ws'
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
const originalVisibilityDescriptor = Object.getOwnPropertyDescriptor(Document.prototype, 'visibilityState')

beforeEach(() => {
  originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  MockWebSocket.instance = null
  useAuthStore.setState({ authDisabled: true, token: null })
})

afterEach(() => {
  globalThis.WebSocket = originalWebSocket
  if (originalVisibilityDescriptor) {
    Object.defineProperty(document, 'visibilityState', originalVisibilityDescriptor)
  }
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

    // scheduleReconnect just armed a real (non-fake-timer) setTimeout since
    // this test does not use vi.useFakeTimers(). Without this close(), that
    // timer survives the test, fires later against the real WebSocket that
    // afterEach restores, and reconnects for real — surfacing as an
    // "Unhandled Rejection: invalid onError method" from undici/jsdom well
    // after this test reported passing (agent-os-o26). close() cancels the
    // pending reconnectTimeout and detaches the recovery listeners.
    client.close()
  })
})

describe('WSClient reconnect jitter', () => {
  it('samples full jitter within [0, capped exponential base] across the reconnect ladder', () => {
    vi.useFakeTimers()
    try {
      const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout')
      const client = new WSClient()
      client.connect('/test', vi.fn())
      MockWebSocket.instance!.onopen!()

      // Attempt n's base is 2000 * 2^(n-1), capped at MAX_RECONNECT_DELAY_MS.
      const expectedBases = [2000, 4000, 8000, 16000, MAX_RECONNECT_DELAY_MS]

      for (const base of expectedBases) {
        setTimeoutSpy.mockClear()
        // The socket never reopens between attempts, so reconnectAttempts
        // keeps climbing instead of resetting on a successful onopen.
        MockWebSocket.instance!.onclose!()
        const delay = setTimeoutSpy.mock.calls.at(-1)![1] as number
        expect(delay).toBeGreaterThanOrEqual(0)
        expect(delay).toBeLessThanOrEqual(base)
        vi.advanceTimersByTime(delay)
      }

      // The last advanceTimersByTime above already fired one more reconnect,
      // leaving client.ws set to a fresh (never-closed) MockWebSocket and the
      // window/document recovery listeners still attached from connect()'s
      // attachRecoveryListeners(). Without close() those listeners leak past
      // this test (agent-os-o26) — attachRecoveryListeners()/detachRecoveryListeners()
      // are the only place they are ever removed.
      client.close()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('WSClient post-cap recovery', () => {
  it('resumes on an online event after the reconnect cap is exhausted', () => {
    vi.useFakeTimers()
    try {
      const onReconnectFailed = vi.fn()
      const client = new WSClient()
      client.connect('/test', vi.fn(), { onReconnectFailed })
      MockWebSocket.instance!.onopen!()

      // 5 failed attempts exhaust the cap; the 6th close is the one that
      // finds the cap already reached and gives up.
      for (let attempt = 0; attempt < 6; attempt++) {
        MockWebSocket.instance!.onclose!()
        if (attempt < 5) vi.advanceTimersByTime(MAX_RECONNECT_DELAY_MS)
      }
      expect(onReconnectFailed).toHaveBeenCalledTimes(1)

      const staleInstance = MockWebSocket.instance
      window.dispatchEvent(new Event('online'))

      expect(MockWebSocket.instance).not.toBe(staleInstance)
      expect(MockWebSocket.instance).not.toBeNull()

      client.close()
    } finally {
      vi.useRealTimers()
    }
  })

  it('resumes on a visibilitychange event after the reconnect cap is exhausted', () => {
    vi.useFakeTimers()
    try {
      const onReconnectFailed = vi.fn()
      const client = new WSClient()
      client.connect('/test', vi.fn(), { onReconnectFailed })
      MockWebSocket.instance!.onopen!()

      for (let attempt = 0; attempt < 6; attempt++) {
        MockWebSocket.instance!.onclose!()
        if (attempt < 5) vi.advanceTimersByTime(MAX_RECONNECT_DELAY_MS)
      }
      expect(onReconnectFailed).toHaveBeenCalledTimes(1)

      const staleInstance = MockWebSocket.instance
      Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
      document.dispatchEvent(new Event('visibilitychange'))

      expect(MockWebSocket.instance).not.toBe(staleInstance)
      expect(MockWebSocket.instance).not.toBeNull()

      client.close()
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not resume while the tab is hidden', () => {
    vi.useFakeTimers()
    try {
      const onReconnectFailed = vi.fn()
      const client = new WSClient()
      client.connect('/test', vi.fn(), { onReconnectFailed })
      MockWebSocket.instance!.onopen!()

      for (let attempt = 0; attempt < 6; attempt++) {
        MockWebSocket.instance!.onclose!()
        if (attempt < 5) vi.advanceTimersByTime(MAX_RECONNECT_DELAY_MS)
      }
      expect(onReconnectFailed).toHaveBeenCalledTimes(1)

      const staleInstance = MockWebSocket.instance
      Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
      document.dispatchEvent(new Event('visibilitychange'))

      expect(MockWebSocket.instance).toBe(staleInstance)

      client.close()
    } finally {
      vi.useRealTimers()
    }
  })
})
