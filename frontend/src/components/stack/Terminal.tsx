import { useEffect, useEffectEvent, useRef, useState, useCallback } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { useWebSocketBinary } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import { TerminalToolbar } from '@/components/stack/TerminalToolbar'
import { TerminalSearchBar } from '@/components/stack/TerminalSearchBar'
import { EmptyState } from '@/components/EmptyState'
import { TerminalSquare } from 'lucide-react'
import type { Stack } from '@/types'

const SESSION_WARNING_MINUTES = 25

const FONT_SIZE_KEY = 'terminal-font-size'
const DEFAULT_FONT_SIZE = 14
const MIN_FONT_SIZE = 10
const MAX_FONT_SIZE = 24

interface TerminalProps {
  stack: Stack
  initialContainer?: string
}

export function TerminalComponent({ stack, initialContainer }: TerminalProps) {
  const [selectedContainer, setSelectedContainer] = useState<string>(initialContainer || '')
  const [isConnected, setIsConnected] = useState(false)
  const [isConnecting, setIsConnecting] = useState(false)
  const [sessionDuration, setSessionDuration] = useState(0)
  const [disconnectCountdown, setDisconnectCountdown] = useState<number | null>(null)
  const [hasSelection, setHasSelection] = useState(false)
  const [fontSize, setFontSize] = useState(() => {
    const stored = localStorage.getItem(FONT_SIZE_KEY)
    const parsed = stored ? parseInt(stored, 10) : DEFAULT_FONT_SIZE
    return Number.isNaN(parsed) ? DEFAULT_FONT_SIZE : Math.max(MIN_FONT_SIZE, Math.min(MAX_FONT_SIZE, parsed))
  })
  const [showHints, setShowHints] = useState(() => {
    const stored = localStorage.getItem('terminal-hints-shown')
    return stored !== 'true'
  })
  const [showSearch, setShowSearch] = useState(false)
  const [searchAddonInstance, setSearchAddonInstance] = useState<SearchAddon | null>(null)
  const terminalRef = useRef<HTMLDivElement>(null)
  const xtermRef = useRef<XTerm | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const textEncoderRef = useRef(new TextEncoder())
  const inactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const disconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const sessionTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const reconnectKeyRef = useRef(0)

  const runningContainers = stack.containers?.filter(c => c.state === 'running') || []

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
        setDisconnectCountdown(null)
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

  const clearInactivityTimers = useCallback(() => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current)
      inactivityTimerRef.current = null
    }
    if (disconnectTimerRef.current) {
      clearTimeout(disconnectTimerRef.current)
      disconnectTimerRef.current = null
    }
  }, [])

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

  const resetInactivityTimer = useCallback(() => {
    clearInactivityTimers()
    inactivityTimerRef.current = setTimeout(() => {
      toast.warning('Session will disconnect in 5 minutes', {
        duration: 300000,
      })
      disconnectTimerRef.current = setTimeout(() => {
        let remaining = 60
        setDisconnectCountdown(remaining)
        // A plain closure counter (rather than a functional setState updater)
        // keeps the side effects below out of the updater — they run once,
        // directly in this callback, not inside a function React may re-invoke.
        const countdownInterval = setInterval(() => {
          remaining -= 1
          if (remaining <= 0) {
            clearInterval(countdownInterval)
            setDisconnectCountdown(null)
            toast.error('Session disconnected due to inactivity (30 minutes)')
            disconnect()
            return
          }
          setDisconnectCountdown(remaining)
        }, 1000)
      }, 300000)
    }, SESSION_WARNING_MINUTES * 60 * 1000)
  }, [clearInactivityTimers, disconnect])

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

  const dismissHints = useCallback(() => {
    setShowHints(false)
    localStorage.setItem('terminal-hints-shown', 'true')
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

  const fitTerminal = useCallback(() => {
    const fitAddon = fitAddonRef.current
    const terminal = xtermRef.current
    if (fitAddon && terminal) {
      fitAddon.fit()
      const cols = terminal.cols
      const rows = terminal.rows
      if (isConnected) {
        send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    }
  }, [isConnected, send])

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
      setDisconnectCountdown(null)
      setIsConnected(false)
      setSelectedContainer(value)
      reconnectKeyRef.current++
    }
  }, [selectedContainer, isConnected, disconnect])

  const handleDisconnect = useCallback(() => {
    if (isConnected) {
      disconnect()
      setIsConnected(false)
      setIsConnecting(false)
      setSessionDuration(0)
      setDisconnectCountdown(null)
      const terminal = xtermRef.current
      if (terminal) {
        terminal.writeln('\r\n\x1b[33mTerminal disconnected.\x1b[0m\r\n')
      }
      clearInactivityTimers()
      toast.info('Terminal disconnected')
    }
  }, [isConnected, disconnect, clearInactivityTimers])

  const handleReconnect = useCallback(() => {
    reconnect()
  }, [reconnect])

  useEffect(() => {
    if (!terminalRef.current) return

    const terminal = new XTerm({
      fontSize,
      fontFamily: 'Menlo, Monaco, Consolas, monospace',
      cursorBlink: true,
      cursorStyle: 'bar',
      scrollback: 10000,
      lineHeight: 1.15,
      allowProposedApi: true,
      theme: {
        background: '#1a1a1a',
        foreground: '#d4d4d4',
        cursor: '#ffffff',
        cursorAccent: '#1a1a1a',
        selectionBackground: '#264f78',
        selectionForeground: '#ffffff',
        black: '#000000',
        red: '#cd3131',
        green: '#0dbc79',
        yellow: '#e5e510',
        blue: '#2472c8',
        magenta: '#bc3fbc',
        cyan: '#11a8cd',
        white: '#e5e5e5',
        brightBlack: '#666666',
        brightRed: '#f14c4c',
        brightGreen: '#23d18b',
        brightYellow: '#f5f543',
        brightBlue: '#3b8eea',
        brightMagenta: '#d670d6',
        brightCyan: '#29b8db',
        brightWhite: '#ffffff',
      },
    })

    const fitAddon = new FitAddon()
    const webLinksAddon = new WebLinksAddon()
    const searchAddon = new SearchAddon()
    const unicode11Addon = new Unicode11Addon()

    terminal.loadAddon(fitAddon)
    terminal.loadAddon(webLinksAddon)
    terminal.loadAddon(searchAddon)
    terminal.loadAddon(unicode11Addon)

    terminal.unicode.activeVersion = '6'

    terminal.attachCustomKeyEventHandler((e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.shiftKey) {
        if (e.key.toLowerCase() === 'c' || e.key.toLowerCase() === 'v' || e.key.toLowerCase() === 'f') {
          return false
        }
      }
      return true
    })

    terminal.open(terminalRef.current)

    xtermRef.current = terminal
    fitAddonRef.current = fitAddon
    searchAddonRef.current = searchAddon
    setSearchAddonInstance(searchAddon)
    const handleData = terminal.onData(handleTerminalData)
    const handleSelectionChange = terminal.onSelectionChange(() => {
      setHasSelection(terminal.hasSelection())
    })

    const handleResize = () => {
      const timeout = setTimeout(() => {
        fitAddon.fit()
        const cols = terminal.cols
        const rows = terminal.rows
        if (isConnected) {
          send(JSON.stringify({ type: 'resize', cols, rows }))
        }
      }, 100)
      return () => clearTimeout(timeout)
    }

    fitAddon.fit()

    const resizeObserver = new ResizeObserver(handleResize)
    const container = terminalRef.current
    if (container) {
      resizeObserver.observe(container)
    }

    return () => {
      handleData.dispose()
      handleSelectionChange.dispose()
      resizeObserver.disconnect()
      terminal.dispose()
      clearInactivityTimers()
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [handleTerminalData, isConnected, send, clearInactivityTimers, reconnectKeyRef])

  // `fitTerminal` is only ever read inside the resize sub-handler below, so
  // wrapping it in an Effect Event keeps this effect from re-subscribing the
  // resize/keydown listeners on every render that changes `isConnected`/`send`
  // (fitTerminal's own deps) — see https://react.dev/reference/react/useEffectEvent
  const onWindowResize = useEffectEvent(() => {
    fitTerminal()
  })

  useEffect(() => {
    const handleWindowResize = () => {
      onWindowResize()
    }

    window.addEventListener('resize', handleWindowResize)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('resize', handleWindowResize)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [handleKeyDown])

  // The terminal attaches to a running container; with none, show an explanatory empty state
  // instead of a black void (matching the Logs and Metrics tabs). The xterm box stays mounted
  // but hidden so its lifecycle is not torn down when containers come and go.
  const hasRunningContainers = runningContainers.length > 0

  return (
    <div className="flex h-full flex-col space-y-4">
      {hasRunningContainers && (
      <TerminalToolbar
        selectedContainer={selectedContainer}
        runningContainers={runningContainers}
        onContainerChange={handleContainerChange}
        isConnected={isConnected}
        isConnecting={isConnecting}
        sessionDuration={sessionDuration}
        disconnectCountdown={disconnectCountdown}
        fontSize={fontSize}
        minFontSize={MIN_FONT_SIZE}
        maxFontSize={MAX_FONT_SIZE}
        hasSelection={hasSelection}
        showSearch={showSearch}
        formatDuration={formatDuration}
        onFontSizeChange={handleFontSizeChange}
        onCopy={handleCopy}
        onPaste={handlePaste}
        onToggleSearch={toggleSearch}
        onDisconnect={handleDisconnect}
        onReconnect={handleReconnect}
        onClose={() => setSelectedContainer('')}
      />
      )}
      {showSearch && isConnected && (
        <TerminalSearchBar searchAddon={searchAddonInstance} onClose={() => {
          setShowSearch(false)
          searchAddonRef.current?.clearDecorations()
          searchAddonRef.current?.clearActiveDecoration()
        }} />
      )}
      {!hasRunningContainers && (
        <EmptyState
          icon={<TerminalSquare className="h-12 w-12 text-muted-foreground" />}
          title="No running containers"
          description="The terminal opens a shell inside a running container. Start the stack to use it."
        />
      )}
      <div className={`rounded-lg border bg-terminal-background p-2 ${hasRunningContainers ? '' : 'hidden'}`}>
        <div
          ref={terminalRef}
          className="overflow-hidden"
          style={{ minHeight: '400px' }}
          onContextMenu={handleContextMenu}
        />
      </div>

      {showHints && isConnected && (
        <div className="rounded-lg border bg-muted/50 p-4 text-sm">
          <div className="mb-2 font-medium">Keyboard Shortcuts</div>
          <div className="space-y-1 text-muted-foreground">
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">Shift</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">C</kbd> - Copy selection</div>
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">Shift</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">V</kbd> - Paste from clipboard</div>
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">Shift</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">F</kbd> - Find in terminal</div>
          </div>
          <Button variant="link" size="sm" onClick={dismissHints} className="mt-2 p-0">
            Don't show again
          </Button>
        </div>
      )}
    </div>
  )
}
