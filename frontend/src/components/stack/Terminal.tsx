import { useEffect, useRef, useState, useCallback } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { useWebSocketBinary } from '@/hooks/useWebSocket'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import { RotateCcw, Terminal, Clock, Copy, Clipboard } from 'lucide-react'
import type { Stack } from '@/types'

const SESSION_WARNING_MINUTES = 25

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
  const [showHints, setShowHints] = useState(() => {
    const stored = localStorage.getItem('terminal-hints-shown')
    return stored !== 'true'
  })
  const terminalRef = useRef<HTMLDivElement>(null)
  const xtermRef = useRef<XTerm | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const webLinksAddonRef = useRef<WebLinksAddon | null>(null)
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
        startSessionTimer()
      },
      onClose: () => {
        setIsConnected(false)
        setIsConnecting(false)
        const terminal = xtermRef.current
        if (terminal) {
          terminal.writeln('\r\n\x1b[31mDisconnected. Press Reconnect to continue.\x1b[0m\r\n')
        }
        clearInactivityTimers()
        stopSessionTimer()
      },
      onError: () => {
        setIsConnected(false)
        setIsConnecting(false)
        toast.error('Terminal connection error')
        stopSessionTimer()
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

  const stopSessionTimer = useCallback(() => {
    if (sessionTimerRef.current) {
      clearInterval(sessionTimerRef.current)
      sessionTimerRef.current = null
    }
  }, [])

  const startSessionTimer = useCallback(() => {
    stopSessionTimer()
    sessionTimerRef.current = setInterval(() => {
      setSessionDuration((prev) => prev + 1)
    }, 1000)
  }, [stopSessionTimer])

  const resetInactivityTimer = useCallback(() => {
    clearInactivityTimers()
    inactivityTimerRef.current = setTimeout(() => {
      toast.warning('Session will disconnect in 5 minutes', {
        duration: 300000,
      })
      disconnectTimerRef.current = setTimeout(() => {
        setDisconnectCountdown(60)
        const countdownInterval = setInterval(() => {
          setDisconnectCountdown((prev) => {
            if (prev === null || prev <= 1) {
              clearInterval(countdownInterval)
              toast.error('Session disconnected due to inactivity (30 minutes)')
              disconnect()
              return null
            }
            return prev - 1
          })
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
    if ((e.ctrlKey || e.metaKey) && e.key === 'c') {
      const terminal = xtermRef.current
      if (terminal && terminal.hasSelection()) {
        e.preventDefault()
        handleCopy()
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 'v') {
      e.preventDefault()
      handlePaste()
    }
  }, [handleCopy, handlePaste])

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
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
      disconnect()
      setSessionDuration(0)
      setDisconnectCountdown(null)
      setSelectedContainer(value)
      reconnectKeyRef.current++
    }
  }, [selectedContainer, disconnect])

  const handleReconnect = useCallback(() => {
    reconnect()
  }, [reconnect])

  useEffect(() => {
    if (!terminalRef.current) return

    const terminal = new XTerm({
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, Consolas, monospace',
      theme: {
        background: '#1a1a1a',
        foreground: '#d4d4d4',
        cursor: '#ffffff',
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

    terminal.loadAddon(fitAddon)
    terminal.loadAddon(webLinksAddon)
    terminal.open(terminalRef.current)

    xtermRef.current = terminal
    fitAddonRef.current = fitAddon
    webLinksAddonRef.current = webLinksAddon

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
      stopSessionTimer()
    }
  }, [handleTerminalData, isConnected, send, clearInactivityTimers, stopSessionTimer, reconnectKeyRef])

  useEffect(() => {
    const handleWindowResize = () => {
      fitTerminal()
    }

    window.addEventListener('resize', handleWindowResize)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('resize', handleWindowResize)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [fitTerminal, handleKeyDown])

  return (
    <div className="flex h-full flex-col space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center space-x-4">
          <Terminal className="h-5 w-5 text-muted-foreground" />
          <Select value={selectedContainer} onValueChange={handleContainerChange}>
            <SelectTrigger className="w-[300px]">
              <SelectValue placeholder="Select container">
                {selectedContainer
                  ? runningContainers.find(c => c.id === selectedContainer)?.name || 'Unknown'
                  : 'Select container'}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {runningContainers.length === 0 ? (
                <div className="px-2 py-2 text-sm text-muted-foreground">
                  No running containers
                </div>
              ) : (
                runningContainers.map(container => (
                  <SelectItem key={container.id} value={container.id}>
                    {container.name}
                  </SelectItem>
                ))
              )}
            </SelectContent>
          </Select>
        </div>
        <div className="flex items-center space-x-2">
          {isConnected && (
            <>
              {disconnectCountdown !== null ? (
                <span className="flex items-center text-sm text-red-500 font-medium">
                  <Clock className="mr-1.5 h-4 w-4" />
                  Disconnecting in {disconnectCountdown} seconds
                </span>
              ) : (
                <span className="flex items-center text-sm text-muted-foreground">
                  <Clock className="mr-1.5 h-4 w-4" />
                  Active for {formatDuration(sessionDuration)}
                </span>
              )}
              <span className="flex items-center text-sm text-muted-foreground">
                <span className="mr-1.5 h-2 w-2 rounded-full bg-green-500" />
                Connected
              </span>
              <Button variant="ghost" size="sm" onClick={handleCopy} disabled={!hasSelection} title="Copy (Ctrl+C)">
                <Copy className="h-4 w-4" />
              </Button>
              <Button variant="ghost" size="sm" onClick={handlePaste} title="Paste (Ctrl+V)">
                <Clipboard className="h-4 w-4" />
              </Button>
            </>
          )}
          {isConnecting && (
            <span className="flex items-center text-sm text-muted-foreground">
              <span className="mr-1.5 h-2 w-2 animate-pulse rounded-full bg-yellow-500" />
              Connecting...
            </span>
          )}
          {!isConnected && !isConnecting && selectedContainer && (
            <Button variant="outline" size="sm" onClick={handleReconnect}>
              <RotateCcw className="mr-2 h-4 w-4" />
              Reconnect
            </Button>
          )}
        </div>
      </div>
      <div
        ref={terminalRef}
        className="flex-1 overflow-hidden rounded-lg border bg-[#1a1a1a]"
        style={{ minHeight: '400px' }}
        onContextMenu={handleContextMenu}
      />

      {showHints && isConnected && (
        <div className="rounded-lg border bg-muted/50 p-4 text-sm">
          <div className="mb-2 font-medium">Keyboard Shortcuts</div>
          <div className="space-y-1 text-muted-foreground">
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">C</kbd> - Copy selection</div>
            <div><kbd className="rounded border bg-background px-1.5 py-0.5">Ctrl</kbd> + <kbd className="rounded border bg-background px-1.5 py-0.5">V</kbd> - Paste from clipboard</div>
          </div>
          <Button variant="link" size="sm" onClick={dismissHints} className="mt-2 p-0">
            Don't show again
          </Button>
        </div>
      )}
    </div>
  )
}
