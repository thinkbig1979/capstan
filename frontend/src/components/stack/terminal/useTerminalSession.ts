import { useCallback, useEffect, useRef, useState } from 'react'
import type { Terminal as XTerm } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import type { SearchAddon } from '@xterm/addon-search'
import { useWebSocketBinary } from '@/hooks/useWebSocket'
import { toast } from 'sonner'
import type { Stack } from '@/types'
import { useXtermLifecycle } from './useXtermLifecycle'
import { useInactivityTimer } from './useInactivityTimer'
import { DEFAULT_FONT_SIZE, FONT_SIZE_KEY, HINTS_SHOWN_KEY, MAX_FONT_SIZE, MIN_FONT_SIZE } from './constants'

interface UseTerminalSessionParams {
  stack: Stack
  initialContainer?: string
}

// All terminal-session state, refs, handlers, and derived values live here,
// called once from the Terminal composition root. The xterm instance's own
// imperative lifecycle (creation/dispose, resize/keydown listeners) is
// delegated to useXtermLifecycle; the inactivity-warning/countdown chain is
// delegated to useInactivityTimer. Everything else — WS wiring, font size,
// hints, copy/paste, container switching, disconnect/reconnect — stays here
// since it all reads/writes the same xterm/addon refs.
export function useTerminalSession({ stack, initialContainer }: UseTerminalSessionParams) {
  const [selectedContainer, setSelectedContainer] = useState<string>(initialContainer || '')
  const [isConnected, setIsConnected] = useState(false)
  const [isConnecting, setIsConnecting] = useState(false)
  const [sessionDuration, setSessionDuration] = useState(0)
  const [fontSize, setFontSize] = useState(() => {
    const stored = localStorage.getItem(FONT_SIZE_KEY)
    const parsed = stored ? parseInt(stored, 10) : DEFAULT_FONT_SIZE
    return Number.isNaN(parsed) ? DEFAULT_FONT_SIZE : Math.max(MIN_FONT_SIZE, Math.min(MAX_FONT_SIZE, parsed))
  })
  const [showHints, setShowHints] = useState(() => {
    const stored = localStorage.getItem(HINTS_SHOWN_KEY)
    return stored !== 'true'
  })
  const [showSearch, setShowSearch] = useState(false)

  const terminalRef = useRef<HTMLDivElement>(null)
  const xtermRef = useRef<XTerm | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const textEncoderRef = useRef(new TextEncoder())
  const sessionTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const reconnectKeyRef = useRef(0)

  const runningContainers = stack.containers?.filter((c) => c.state === 'running') || []

  const handleBinaryMessage = useCallback((data: ArrayBuffer) => {
    const terminal = xtermRef.current
    if (terminal) {
      terminal.write(new Uint8Array(data))
    }
  }, [])

  const { send, disconnect, reconnect } = useWebSocketBinary(
    selectedContainer ? `/ws/terminal/${stack.id}/${selectedContainer}` : '',
    handleBinaryMessage,
    {
      skip: !selectedContainer,
      onOpen: () => {
        setIsConnected(true)
        setIsConnecting(false)
        setSessionDuration(0)
        clearDisconnectCountdown()
        toast.success('Terminal connected')
        resetInactivityTimer()
      },
      onClose: () => {
        setIsConnected(false)
        setIsConnecting(false)
        const terminal = xtermRef.current
        if (terminal) {
          terminal.writeln('\r\n\x1b[31mDisconnected. Press Reconnect to continue.\x1b[0m\r\n')
        }
        clearInactivityTimers()
      },
      onError: () => {
        setIsConnected(false)
        setIsConnecting(false)
        toast.error('Terminal connection error')
      },
    },
  )

  const { disconnectCountdown, resetInactivityTimer, clearInactivityTimers, clearDisconnectCountdown } =
    useInactivityTimer(disconnect)

  useEffect(() => {
    if (isConnected) {
      sessionTimerRef.current = setInterval(() => {
        setSessionDuration((prev) => prev + 1)
      }, 1000)
      return () => {
        if (sessionTimerRef.current) {
          clearInterval(sessionTimerRef.current)
          sessionTimerRef.current = null
        }
      }
    }
  }, [isConnected])

  const formatDuration = useCallback((seconds: number): string => {
    const hours = Math.floor(seconds / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    const secs = seconds % 60
    if (hours > 0) {
      return `${hours}:${minutes.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`
    }
    return `${minutes}:${secs.toString().padStart(2, '0')}`
  }, [])

  const handleCopy = useCallback(async () => {
    const terminal = xtermRef.current
    if (terminal && terminal.hasSelection()) {
      const selection = terminal.getSelection()
      try {
        await navigator.clipboard.writeText(selection)
        toast.success('Copied to clipboard')
      } catch {
        toast.error('Failed to copy to clipboard')
      }
    }
  }, [])

  const handlePaste = useCallback(async () => {
    try {
      const text = await navigator.clipboard.readText()
      if (text && isConnected) {
        send(textEncoderRef.current.encode(text).buffer)
        resetInactivityTimer()
      }
    } catch {
      toast.error('Failed to paste from clipboard')
    }
  }, [isConnected, send, resetInactivityTimer])

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === 'c') {
      const terminal = xtermRef.current
      if (terminal && terminal.hasSelection()) {
        e.preventDefault()
        e.stopPropagation()
        handleCopy()
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === 'v') {
      e.preventDefault()
      e.stopPropagation()
      handlePaste()
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === 'f') {
      e.preventDefault()
      e.stopPropagation()
      setShowSearch(true)
    }
  }, [handleCopy, handlePaste])

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
  }, [])

  const handleFontSizeChange = useCallback((delta: number) => {
    const next = Math.max(MIN_FONT_SIZE, Math.min(MAX_FONT_SIZE, fontSize + delta))
    setFontSize(next)
    // Side effects run here, after the setState call, rather than inside a
    // functional updater — updaters should stay pure since React may re-invoke them.
    const terminal = xtermRef.current
    if (terminal) {
      terminal.options.fontSize = next
      fitAddonRef.current?.fit()
    }
    localStorage.setItem(FONT_SIZE_KEY, String(next))
  }, [fontSize])

  const toggleSearch = useCallback(() => {
    setShowSearch((prev) => {
      if (prev) {
        searchAddonRef.current?.clearDecorations()
        searchAddonRef.current?.clearActiveDecoration()
      }
      return !prev
    })
  }, [])

  const handleCloseSearch = useCallback(() => {
    setShowSearch(false)
    searchAddonRef.current?.clearDecorations()
    searchAddonRef.current?.clearActiveDecoration()
  }, [])

  const dismissHints = useCallback(() => {
    setShowHints(false)
    localStorage.setItem(HINTS_SHOWN_KEY, 'true')
  }, [])

  const handleTerminalData = useCallback(
    (data: string) => {
      if (isConnected) {
        send(textEncoderRef.current.encode(data).buffer)
        resetInactivityTimer()
      }
    },
    [isConnected, send, resetInactivityTimer],
  )

  const handleContainerChange = useCallback((value: string) => {
    if (value !== selectedContainer) {
      if (isConnected) {
        const terminal = xtermRef.current
        if (terminal) {
          terminal.writeln('\r\n\x1b[33mSwitching terminal to a different container...\x1b[0m\r\n')
        }
        disconnect()
      }
      setSessionDuration(0)
      clearDisconnectCountdown()
      setIsConnected(false)
      setSelectedContainer(value)
      reconnectKeyRef.current++
    }
  }, [selectedContainer, isConnected, disconnect, clearDisconnectCountdown])

  const handleDisconnect = useCallback(() => {
    if (isConnected) {
      disconnect()
      setIsConnected(false)
      setIsConnecting(false)
      setSessionDuration(0)
      clearDisconnectCountdown()
      const terminal = xtermRef.current
      if (terminal) {
        terminal.writeln('\r\n\x1b[33mTerminal disconnected.\x1b[0m\r\n')
      }
      clearInactivityTimers()
      toast.info('Terminal disconnected')
    }
  }, [isConnected, disconnect, clearInactivityTimers, clearDisconnectCountdown])

  const handleReconnect = useCallback(() => {
    reconnect()
  }, [reconnect])

  const handleClose = useCallback(() => {
    setSelectedContainer('')
  }, [])

  const { hasSelection, searchAddonInstance } = useXtermLifecycle({
    terminalRef,
    xtermRef,
    fitAddonRef,
    searchAddonRef,
    reconnectKeyRef,
    fontSize,
    handleTerminalData,
    isConnected,
    send,
    clearInactivityTimers,
    handleKeyDown,
  })

  // The terminal attaches to a running container; with none, show an explanatory empty state
  // instead of a black void (matching the Logs and Metrics tabs). The xterm box stays mounted
  // but hidden so its lifecycle is not torn down when containers come and go.
  const hasRunningContainers = runningContainers.length > 0

  return {
    selectedContainer,
    isConnected,
    isConnecting,
    sessionDuration,
    disconnectCountdown,
    hasSelection,
    fontSize,
    showHints,
    showSearch,
    searchAddonInstance,
    terminalRef,
    runningContainers,
    hasRunningContainers,
    formatDuration,
    handleContainerChange,
    handleFontSizeChange,
    handleCopy,
    handlePaste,
    toggleSearch,
    handleDisconnect,
    handleReconnect,
    handleClose,
    handleCloseSearch,
    dismissHints,
    handleContextMenu,
  }
}
