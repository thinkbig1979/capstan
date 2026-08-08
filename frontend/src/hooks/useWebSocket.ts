import { useEffect, useMemo, useRef, useState, useCallback } from 'react'
import { useAuthStore } from '@/stores/authStore'
import { WSClient, type WSClientOptions, type WSState } from '@/lib/ws'

export interface UseWebSocketOptions extends WSClientOptions {
  skip?: boolean
}

export interface UseWebSocketReturn {
  status: 'connecting' | 'connected' | 'disconnected' | 'reconnecting'
  send: (data: string | ArrayBuffer) => void
  lastMessage: string | ArrayBuffer | null
  reconnect: () => void
  disconnect: () => void
  wsState: WSState
  reconnectAttempts: number
}

export function useWebSocket(
  path: string,
  onMessage: (data: string | ArrayBuffer) => void,
  options: UseWebSocketOptions = {}
): UseWebSocketReturn {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const authDisabled = useAuthStore((s) => s.authDisabled)
  const wsClientRef = useRef<WSClient | null>(null)
  const onMessageRef = useRef(onMessage)
  const optionsRef = useRef(options)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Read off `options` rather than a ref: this is the one option the connect
  // effect must react to, so it has to be a real dependency (agent-os-9d5e).
  const skip = options.skip ?? false
  const [lastMessage, setLastMessage] = useState<string | ArrayBuffer | null>(null)
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'reconnecting'>('disconnected')
  const [wsState, setWsState] = useState<WSState>('CLOSED')
  const [reconnectAttempts, setReconnectAttempts] = useState(0)

  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  useEffect(() => {
    optionsRef.current = options
  }, [options])

  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    setLastMessage(data)
    onMessageRef.current(data)
  }, [])

  /**
   * Layers the hook's state updates onto the caller's lifecycle callbacks.
   *
   * Every connect must go through this. The initial connect and the manual
   * reconnect used to build it separately, and reconnect's copy was simply
   * missing — it connected with the raw caller options, so the reopened socket
   * updated no state and the UI sat on 'connecting' forever (agent-os-9d5e).
   * Keeping exactly one builder is what makes that class of drift impossible.
   */
  const buildEnhancedOptions = useCallback((opts: UseWebSocketOptions): WSClientOptions => {
    return {
      ...opts,
      onOpen: () => {
        setStatus('connected')
        setWsState('OPEN')
        opts.onOpen?.()
      },
      onClose: (event) => {
        setStatus('disconnected')
        setWsState('CLOSED')
        opts.onClose?.(event)
      },
      onError: (error) => {
        setStatus('disconnected')
        opts.onError?.(error)
      },
      onReconnecting: (attempt) => {
        setStatus('reconnecting')
        setWsState('RECONNECTING')
        setReconnectAttempts(attempt)
        opts.onReconnecting?.(attempt)
      },
      onReconnectFailed: () => {
        setStatus('disconnected')
        setWsState('CLOSED')
        opts.onReconnectFailed?.()
      },
    }
  }, [])

  useEffect(() => {
    if ((!isAuthenticated && !authDisabled) || skip) {
      return
    }

    const client = new WSClient()
    wsClientRef.current = client

    const connectingTimeout = setTimeout(() => {
      setStatus('connecting')
      setWsState('CONNECTING')
    }, 0)
    client.connect(path, wrappedOnMessage, buildEnhancedOptions(optionsRef.current))

    return () => {
      clearTimeout(connectingTimeout)
      // A manual reconnect re-opens on a timer; if the effect re-runs (path,
      // auth or skip changed) before it fires, that timer would otherwise
      // connect the replacement client to the old path.
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      client.close()
      wsClientRef.current = null
      setStatus('disconnected')
      setWsState('CLOSED')
      setReconnectAttempts(0)
    }
  }, [path, isAuthenticated, authDisabled, skip, wrappedOnMessage, buildEnhancedOptions])

  const send = useCallback((data: string | ArrayBuffer) => {
    wsClientRef.current?.send(data)
  }, [])

  const reconnect = useCallback(() => {
    if ((!isAuthenticated && !authDisabled) || skip) {
      return
    }
    const enhancedOptions = buildEnhancedOptions(optionsRef.current)
    wsClientRef.current?.close()
    setStatus('connecting')
    setWsState('CONNECTING')
    if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
    reconnectTimerRef.current = setTimeout(() => {
      reconnectTimerRef.current = null
      wsClientRef.current?.connect(path, wrappedOnMessage, enhancedOptions)
    }, 100)
  }, [path, isAuthenticated, authDisabled, skip, wrappedOnMessage, buildEnhancedOptions])

  const disconnect = useCallback(() => {
    wsClientRef.current?.close()
  }, [])

  return {
    status,
    send,
    lastMessage,
    reconnect,
    disconnect,
    wsState,
    reconnectAttempts,
  }
}

export interface UseWebSocketJSONOptions<T> extends Omit<UseWebSocketOptions, 'binary'> {
  parse?: (data: string) => T
}

export interface UseWebSocketJSONReturn<T> extends Omit<UseWebSocketReturn, 'lastMessage'> {
  lastMessage: T | null
}

export function useWebSocketJSON<T>(
  path: string,
  onMessage: (data: T) => void,
  options: UseWebSocketJSONOptions<T> = {}
): UseWebSocketJSONReturn<T> {
  const [lastMessage, setLastMessage] = useState<T | null>(null)
  const onMessageRef = useRef(onMessage)
  const parseRef = useRef(options.parse)

  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  useEffect(() => {
    parseRef.current = options.parse
  }, [options.parse])

  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    if (typeof data !== 'string') {
      console.warn('Expected string data for JSON parsing, got ArrayBuffer')
      return
    }

    try {
      const parsed = parseRef.current ? parseRef.current(data) : JSON.parse(data) as T
      setLastMessage(parsed)
      onMessageRef.current(parsed)
    } catch (error) {
      console.warn('Failed to parse WebSocket message as JSON:', error)
    }
  }, [])

  const wsOptions = useMemo<UseWebSocketOptions>(
    () => ({ ...options, binary: false }),
    [options],
  )
  const ws = useWebSocket(path, wrappedOnMessage, wsOptions)

  return {
    ...ws,
    lastMessage,
  }
}

export function useWebSocketBinary(
  path: string,
  onMessage: (data: ArrayBuffer) => void,
  options: UseWebSocketOptions = {}
): UseWebSocketReturn {
  const wrappedOnMessage = useCallback((data: string | ArrayBuffer) => {
    if (typeof data === 'string') {
      console.warn('Expected ArrayBuffer data, got string')
      return
    }
    onMessage(data)
  }, [onMessage])

  const wsOptions = useMemo<UseWebSocketOptions>(
    () => ({ ...options, binary: true }),
    [options],
  )
  return useWebSocket(path, wrappedOnMessage, wsOptions)
}
