import { useEffect, useRef, useState, useCallback } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { useWebSocketBinary } from '@/hooks/useWebSocket'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import { RotateCcw, Terminal as TerminalIcon } from 'lucide-react'
import type { Stack } from '@/types'

interface TerminalProps {
  stack: Stack
  initialContainer?: string
}

export function Terminal({ stack, initialContainer }: TerminalProps) {
  const [selectedContainer, setSelectedContainer] = useState<string>(initialContainer || '')
  const [isConnected, setIsConnected] = useState(false)
  const [isConnecting, setIsConnecting] = useState(false)
  const terminalRef = useRef<HTMLDivElement>(null)
  const xtermRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const webLinksAddonRef = useRef<WebLinksAddon | null>(null)
  const textEncoderRef = useRef(new TextEncoder())
  const inactivityTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const disconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
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

  const resetInactivityTimer = useCallback(() => {
    clearInactivityTimers()
    inactivityTimerRef.current = setTimeout(() => {
      toast.warning('Session inactive for 25 minutes. Will disconnect in 5 minutes.', {
        duration: 300000,
      })
      disconnectTimerRef.current = setTimeout(() => {
        toast.error('Session disconnected due to inactivity (30 minutes)')
        disconnect()
      }, 300000)
    }, 1500000)
  }, [clearInactivityTimers, disconnect])

  const handleTerminalData = useCallback(
    (data: string) => {
      if (isConnected) {
        send(textEncoderRef.current.encode(data))
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
      setSelectedContainer(value)
      reconnectKeyRef.current++
    }
  }, [selectedContainer, disconnect])

  const handleReconnect = useCallback(() => {
    reconnect()
  }, [reconnect])

  useEffect(() => {
    if (!terminalRef.current) return

    const terminal = new Terminal({
      cursorBlink: true,
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
      resizeObserver.disconnect()
      terminal.dispose()
      clearInactivityTimers()
    }
  }, [handleTerminalData, isConnected, send, clearInactivityTimers, reconnectKeyRef])

  useEffect(() => {
    const handleWindowResize = () => {
      fitTerminal()
    }

    window.addEventListener('resize', handleWindowResize)
    return () => {
      window.removeEventListener('resize', handleWindowResize)
    }
  }, [fitTerminal])

  return (
    <div className="flex h-full flex-col space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center space-x-4">
          <TerminalIcon className="h-5 w-5 text-muted-foreground" />
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
            <span className="flex items-center text-sm text-muted-foreground">
              <span className="mr-1.5 h-2 w-2 rounded-full bg-green-500" />
              Connected
            </span>
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
      />
    </div>
  )
}
