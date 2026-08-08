import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useAuthStore } from '@/stores/authStore'
import { WSClient } from '@/lib/ws'
import { useWebSocket } from '../useWebSocket'

class MockWebSocket {
  url: string
  readyState: number
  onopen: (() => void) | null
  onclose: (() => void) | null
  onmessage: ((e: { data: string }) => void) | null
  onerror: ((e: Event) => void) | null
  static OPEN = 1
  static CLOSED = 3
  static CONNECTING = 0

  constructor(url: string) {
    this.url = url
    this.readyState = 0
    this.onopen = null
    this.onclose = null
    this.onmessage = null
    this.onerror = null
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

const stableOptions: Record<string, unknown> = {}

describe('useWebSocket auth-disabled behavior', () => {
  describe('hook connection guard', () => {
    it('connects when authDisabled=true and isAuthenticated=false', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })
      expect(MockWebSocket.instance).not.toBeNull()
    })

    it('does not connect when authDisabled=false and isAuthenticated=false', () => {
      useAuthStore.setState({ authDisabled: false, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      expect(result.current.wsState).toBe('CLOSED')
      expect(result.current.status).toBe('disconnected')
      expect(MockWebSocket.instance).toBeNull()
    })

    it('connects when isAuthenticated=true regardless of authDisabled=false', async () => {
      useAuthStore.setState({
        isAuthenticated: true,
        authDisabled: false,
        token: 'valid.jwt.token',
      })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })
      expect(MockWebSocket.instance).not.toBeNull()
    })

    it('connects when isAuthenticated=true and authDisabled=true', async () => {
      useAuthStore.setState({
        isAuthenticated: true,
        authDisabled: true,
        token: 'valid.jwt.token',
      })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })
      expect(MockWebSocket.instance).not.toBeNull()
    })

    it('respects skip option and does not connect', () => {
      useAuthStore.setState({
        isAuthenticated: true,
        authDisabled: false,
        token: 'valid.jwt.token',
      })

      const onMessage = vi.fn()
      const skipOptions = { skip: true }
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, skipOptions)
      )

      expect(result.current.wsState).toBe('CLOSED')
      expect(MockWebSocket.instance).toBeNull()
    })
  })

  describe('WSClient URL construction', () => {
    it('omits token from URL when authDisabled=true and no token exists', () => {
      useAuthStore.setState({ authDisabled: true, token: null, isAuthenticated: false })

      const client = new WSClient()
      client.connect('/containers', vi.fn())

      expect(MockWebSocket.instance).not.toBeNull()
      expect(MockWebSocket.instance!.url).not.toContain('?token=')
      expect(MockWebSocket.instance!.url).toMatch(/\/api\/v1\/containers$/)
    })

    it('sends auth message as first message when token exists', () => {
      useAuthStore.setState({
        authDisabled: false,
        token: 'my.jwt.token',
        isAuthenticated: true,
      })

      const sendSpy = vi.fn()
      const client = new WSClient()
      client.connect('/containers', vi.fn())

      expect(MockWebSocket.instance).not.toBeNull()
      expect(MockWebSocket.instance!.url).not.toContain('?token=')

      MockWebSocket.instance!.send = sendSpy
      MockWebSocket.instance!.onopen!()

      expect(sendSpy).toHaveBeenCalledWith(JSON.stringify({ type: 'auth', token: 'my.jwt.token' }))
    })

    it('does not send auth message when authDisabled=true without token', () => {
      useAuthStore.setState({
        authDisabled: true,
        token: null,
        isAuthenticated: false,
      })

      const sendSpy = vi.fn()
      const client = new WSClient()
      client.connect('/containers', vi.fn())

      MockWebSocket.instance!.send = sendSpy
      MockWebSocket.instance!.onopen!()

      expect(sendSpy).not.toHaveBeenCalled()
    })

    it('does not connect when authDisabled=false and no token', () => {
      useAuthStore.setState({ authDisabled: false, token: null, isAuthenticated: false })

      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
      const client = new WSClient()
      client.connect('/containers', vi.fn())

      expect(MockWebSocket.instance).toBeNull()
      expect(consoleSpy).toHaveBeenCalledWith('Cannot connect: not authenticated')
      consoleSpy.mockRestore()
    })
  })

  describe('lifecycle callbacks', () => {
    it('transitions to connected on open', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      expect(result.current.status).toBe('connected')
      expect(result.current.wsState).toBe('OPEN')
    })

    it('transitions to disconnected then reconnecting on close', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onReconnecting = vi.fn()
      const reconnectOptions = { onReconnecting }
      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, reconnectOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      expect(result.current.status).toBe('connected')

      act(() => {
        MockWebSocket.instance!.onclose!()
      })

      expect(result.current.wsState).toBe('RECONNECTING')
      expect(result.current.reconnectAttempts).toBe(1)
      expect(onReconnecting).toHaveBeenCalledWith(1)
    })

    it('transitions to disconnected on error', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const onError = vi.fn()
      const errorOptions = { onError }
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, errorOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onerror!(new Event('error'))
      })

      expect(result.current.status).toBe('disconnected')
      expect(onError).toHaveBeenCalledTimes(1)
    })

    it('receives messages and updates lastMessage', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      act(() => {
        MockWebSocket.instance!.onmessage!({ data: 'hello world' })
      })

      expect(result.current.lastMessage).toBe('hello world')
      expect(onMessage).toHaveBeenCalledWith('hello world')
    })

    it('sends data through the client', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.readyState = MockWebSocket.OPEN
        MockWebSocket.instance!.onopen!()
      })

      const sendSpy = vi.spyOn(MockWebSocket.instance!, 'send')

      act(() => {
        result.current.send('test payload')
      })

      expect(sendSpy).toHaveBeenCalledWith('test payload')
    })

    it('disconnects on cleanup', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result, unmount } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      expect(result.current.wsState).toBe('OPEN')

      const closeSpy = vi.spyOn(MockWebSocket.instance!, 'close')

      act(() => {
        unmount()
      })

      expect(closeSpy).toHaveBeenCalled()
    })

    it('reconnect re-wires the state callbacks, so the reopened socket reaches connected', async () => {
      // Asserting only that status flips to 'connecting' passes even when
      // reconnect() connects with the raw caller options and no state setters
      // are wired at all — the socket then opens and the UI sits on
      // 'connecting' forever. The assertion that matters is what happens
      // AFTER the reopened socket fires onopen.
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onOpen = vi.fn()
      const onMessage = vi.fn()
      const { result } = renderHook(() => useWebSocket('/containers', onMessage, { onOpen }))

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      const firstSocket = MockWebSocket.instance!
      act(() => {
        firstSocket.onopen!()
      })
      expect(result.current.status).toBe('connected')

      act(() => {
        result.current.reconnect()
      })
      expect(result.current.status).toBe('connecting')

      // reconnect() re-opens on a 100ms timer.
      await waitFor(() => {
        expect(MockWebSocket.instance).not.toBe(firstSocket)
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      expect(result.current.status).toBe('connected')
      expect(result.current.wsState).toBe('OPEN')
      // The caller's own handler still runs, once per open.
      expect(onOpen).toHaveBeenCalledTimes(2)
    })

    it('reconnect keeps the close/reconnecting wiring too', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() => useWebSocket('/containers', onMessage, {}))

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      const firstSocket = MockWebSocket.instance!
      act(() => {
        result.current.reconnect()
      })
      await waitFor(() => {
        expect(MockWebSocket.instance).not.toBe(firstSocket)
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })
      expect(result.current.status).toBe('connected')

      act(() => {
        MockWebSocket.instance!.onclose!()
      })

      expect(result.current.wsState).toBe('RECONNECTING')
      expect(result.current.reconnectAttempts).toBe(1)
    })

    it('flipping skip from true to false connects without a remount', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result, rerender } = renderHook(
        ({ skip }: { skip: boolean }) => useWebSocket('/containers', onMessage, { skip }),
        { initialProps: { skip: true } },
      )

      expect(MockWebSocket.instance).toBeNull()
      expect(result.current.wsState).toBe('CLOSED')

      rerender({ skip: false })

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })
      expect(MockWebSocket.instance).not.toBeNull()

      act(() => {
        MockWebSocket.instance!.onopen!()
      })
      expect(result.current.status).toBe('connected')
    })

    it('flipping skip back to true tears the connection down', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result, rerender } = renderHook(
        ({ skip }: { skip: boolean }) => useWebSocket('/containers', onMessage, { skip }),
        { initialProps: { skip: false } },
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })
      expect(result.current.status).toBe('connected')

      const closeSpy = vi.spyOn(MockWebSocket.instance!, 'close')

      act(() => {
        rerender({ skip: true })
      })

      expect(closeSpy).toHaveBeenCalled()
      expect(result.current.wsState).toBe('CLOSED')
      expect(result.current.status).toBe('disconnected')
    })

    it('reconnect resets status and re-connects', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      expect(result.current.wsState).toBe('OPEN')

      act(() => {
        result.current.reconnect()
      })

      expect(result.current.status).toBe('connecting')
    })

    it('reconnect is no-op when not authenticated and auth not disabled', () => {
      useAuthStore.setState({ authDisabled: false, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      act(() => {
        result.current.reconnect()
      })

      expect(result.current.wsState).toBe('CLOSED')
    })

    it('disconnect closes the underlying client', async () => {
      useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })

      const onMessage = vi.fn()
      const { result } = renderHook(() =>
        useWebSocket('/containers', onMessage, stableOptions)
      )

      await waitFor(() => {
        expect(result.current.wsState).toBe('CONNECTING')
      })

      act(() => {
        MockWebSocket.instance!.onopen!()
      })

      const wsInstance = MockWebSocket.instance!
      const closeSpy = vi.spyOn(wsInstance, 'close')

      act(() => {
        result.current.disconnect()
      })

      expect(closeSpy).toHaveBeenCalled()
    })
  })
})
