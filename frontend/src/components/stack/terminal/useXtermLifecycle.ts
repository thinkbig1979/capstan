import { useCallback, useEffect, useEffectEvent, useState, type RefObject } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'

export interface UseXtermLifecycleParams {
  terminalRef: RefObject<HTMLDivElement | null>
  xtermRef: RefObject<XTerm | null>
  fitAddonRef: RefObject<FitAddon | null>
  searchAddonRef: RefObject<SearchAddon | null>
  // Included in the creation effect's own dependency array below purely to
  // mirror the original component's dependency list — see the quirk note on
  // that array for why bumping `.current` does not actually retrigger it.
  reconnectKeyRef: RefObject<number>
  fontSize: number
  handleTerminalData: (data: string) => void
  isConnected: boolean
  send: (data: string | ArrayBuffer) => void
  clearInactivityTimers: () => void
  handleKeyDown: (e: KeyboardEvent) => void
}

export interface UseXtermLifecycleResult {
  hasSelection: boolean
  searchAddonInstance: SearchAddon | null
}

// Owns xterm's imperative lifecycle: instance creation/teardown, addon
// wiring, and the window-resize/document-keydown listeners. The refs are
// created by the caller (useTerminalSession) rather than here, because
// several handlers there (copy/paste/font-size/disconnect messages) also
// need direct access to the same terminal/addon instances.
export function useXtermLifecycle({
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
}: UseXtermLifecycleParams): UseXtermLifecycleResult {
  const [hasSelection, setHasSelection] = useState(false)
  const [searchAddonInstance, setSearchAddonInstance] = useState<SearchAddon | null>(null)

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
  }, [fitAddonRef, xtermRef, isConnected, send])

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

  return { hasSelection, searchAddonInstance }
}
