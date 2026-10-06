import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WSClient } from '@/lib/ws'
import { useAuthStore } from '@/stores/authStore'

// agent-os-n4ca.2: login and setup return only { user }; the JWT is in the
// HttpOnly cookie. The WS gate must still open a socket straight after login,
// without a reload, and must never put a token in a frame.

const mockLogin = vi.fn()
const mockSetup = vi.fn()

vi.mock('@/lib/api', () => ({
  authApi: {
    login: (...args: unknown[]) => mockLogin(...args),
    setup: (...args: unknown[]) => mockSetup(...args),
  },
}))

class MockWebSocket {
  url: string
  readyState = 0
  binaryType = 'blob'
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  static OPEN = 1
  static CLOSED = 3
  static CONNECTING = 0
  static instance: MockWebSocket | null = null
  sent: string[] = []

  constructor(url: string) {
    this.url = url
    MockWebSocket.instance = this
  }

  send(data: string) {
    this.sent.push(data)
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
  }
}

const user = { id: '1', username: 'admin', createdAt: '', updatedAt: '' }
let originalWebSocket: typeof WebSocket

beforeEach(() => {
  vi.clearAllMocks()
  originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  MockWebSocket.instance = null
  useAuthStore.setState({
    token: null,
    user: null,
    isAuthenticated: false,
    authDisabled: false,
    needsSetup: false,
  })
})

afterEach(() => {
  globalThis.WebSocket = originalWebSocket
})

describe.each([
  ['login', () => {
    mockLogin.mockResolvedValue({ user })
    return useAuthStore.getState().login('admin', 'password')
  }],
  ['setup', () => {
    mockSetup.mockResolvedValue({ user })
    return useAuthStore.getState().setup('admin', 'password')
  }],
])('WSClient after %s with a token-less response', (_name, authenticate) => {
  it('opens the socket and sends no auth frame', async () => {
    await authenticate()

    const client = new WSClient()
    const connected = client.connect('/ws/test', vi.fn())

    expect(connected).toBe(true)
    expect(MockWebSocket.instance).not.toBeNull()

    MockWebSocket.instance!.onopen!()
    expect(MockWebSocket.instance!.sent).toEqual([])

    client.close()
  })
})
