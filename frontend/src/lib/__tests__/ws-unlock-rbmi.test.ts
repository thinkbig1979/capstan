import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { WSClient, WS_CLOSE_UNLOCK_REQUIRED } from '@/lib/ws'
import { useAuthStore } from '@/stores/authStore'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

// agent-os-rbmi: a browser cannot set X-Unlock-Token on an upgrade, so a
// socket that needs the env unlock offers the token as a subprotocol.

class MockWebSocket {
  url: string
  protocols: string | string[] | undefined
  readyState = 0
  onopen: (() => void) | null = null
  onclose: ((e?: { code: number }) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  static OPEN = 1
  static CLOSED = 3
  static CONNECTING = 0
  static instances: MockWebSocket[] = []

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols = protocols
    MockWebSocket.instances.push(this)
  }
  send() {}
  close() {
    this.readyState = MockWebSocket.CLOSED
  }
}

let originalWebSocket: typeof WebSocket

beforeEach(() => {
  vi.useFakeTimers()
  originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  MockWebSocket.instances = []
  useAuthStore.setState({ authDisabled: false, token: 'cookie' })
  useEnvUnlockStore.getState().lock()
})

afterEach(() => {
  useEnvUnlockStore.getState().lock()
  globalThis.WebSocket = originalWebSocket
  vi.useRealTimers()
})

const last = () => MockWebSocket.instances[MockWebSocket.instances.length - 1]

describe('WSClient unlock token', () => {
  it('offers the unlock token as a subprotocol beside "capstan"', () => {
    useEnvUnlockStore.getState().unlock('tok-1')
    new WSClient().connect('/ws/logs/s1', vi.fn(), { sendUnlockToken: true })
    expect(last().protocols).toEqual(['capstan', 'capstan-unlock.tok-1'])
    expect(last().url).not.toContain('tok-1')
  })

  it('offers no subprotocol while locked', () => {
    new WSClient().connect('/ws/logs/s1', vi.fn(), { sendUnlockToken: true })
    expect(last().protocols).toBeUndefined()
  })

  it('CONTROL: a socket that did not ask for the token never carries it', () => {
    useEnvUnlockStore.getState().unlock('tok-1')
    new WSClient().connect('/ws/events', vi.fn())
    expect(last().protocols).toBeUndefined()
  })

  it('reads the token at each dial, so a redial carries the fresh one', () => {
    useEnvUnlockStore.getState().unlock('tok-1')
    new WSClient().connect('/ws/logs/s1', vi.fn(), { sendUnlockToken: true })
    last().onopen!()
    useEnvUnlockStore.getState().unlock('tok-2')
    last().onclose!({ code: 1006 })
    vi.runOnlyPendingTimers()
    expect(MockWebSocket.instances).toHaveLength(2)
    expect(last().protocols).toEqual(['capstan', 'capstan-unlock.tok-2'])
  })
})

describe('WSClient unlock refusals', () => {
  it('does not redial after 4403 (unlock required)', () => {
    const onReconnecting = vi.fn()
    new WSClient().connect('/ws/terminal/s/c', vi.fn(), { onReconnecting })
    last().onopen!()
    last().onclose!({ code: WS_CLOSE_UNLOCK_REQUIRED })
    vi.runOnlyPendingTimers()
    expect(onReconnecting).not.toHaveBeenCalled()
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('redialIfNeverOpened:false does not redial a handshake that failed (a 403 reads as 1006)', () => {
    const onReconnecting = vi.fn()
    new WSClient().connect('/ws/terminal/s/c', vi.fn(), { redialIfNeverOpened: false, onReconnecting })
    last().onclose!({ code: 1006 })
    vi.runOnlyPendingTimers()
    expect(onReconnecting).not.toHaveBeenCalled()
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('CONTROL: redialIfNeverOpened:false still redials a socket that had opened', () => {
    const onReconnecting = vi.fn()
    new WSClient().connect('/ws/terminal/s/c', vi.fn(), { redialIfNeverOpened: false, onReconnecting })
    last().onopen!()
    last().onclose!({ code: 1006 })
    expect(onReconnecting).toHaveBeenCalledWith(1)
  })
})
