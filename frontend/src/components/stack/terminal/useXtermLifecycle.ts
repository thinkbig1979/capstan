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
// wiring, and the window-resize/document-keydown listeners. The terminal is
// created once per mount: connecting and disconnecting must not rebuild it, or
// the scrollback and the "Disconnected" line written into it are lost
// (agent-os-z91e.34). The refs are
// created by the caller (useTerminalSession) rather than here, because
// several handlers there (copy/paste/font-size/disconnect messages) also
// need direct access to the same terminal/addon instances.
export function useXtermLifecycle({
  terminalRef,
  xtermRef,
  fitAddonRef,
  searchAddonRef,
  fontSize,
  handleTerminalData,
  isConnected,
  send,
  clearInactivityTimers,
  handleKeyDown,
}: UseXtermLifecycleParams): UseXtermLifecycleResult {
  const [hasSelection, setHasSelection] = useState(false)
  const [searchAddonInstance, setSearchAddonInstance] = useState<SearchAddon | null>(null)

  // The creation effect below runs once, so its handlers read the latest
  // connection state, `send` and `handleTerminalData` through Effect Events
  // instead of closing over the values from the render that created the terminal.
  const onTerminalData = useEffectEvent((data: string) => {
    handleTerminalData(data)
  })

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

  // `fitTerminal` is only ever read from the resize/connect handlers below, so
  // wrapping it in an Effect Event keeps those effects from re-running whenever
  // `isConnected`/`send` (fitTerminal's own deps) change — see
  // https://react.dev/reference/react/useEffectEvent
  const onFitRequested = useEffectEvent(() => {
    fitTerminal()
  })

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
    const handleData = terminal.onData((data) => onTerminalData(data))
    const handleSelectionChange = terminal.onSelectionChange(() => {
      setHasSelection(terminal.hasSelection())
    })

    // One pending timer: a burst of ResizeObserver callbacks collapses into a
    // single fit, and the cleanup below cancels it so it cannot fire against a
    // disposed terminal. (The callback's return value is ignored by the
    // observer, so a clearTimeout returned from it never ran.)
    let resizeTimeout: ReturnType<typeof setTimeout> | undefined
    const handleResize = () => {
      clearTimeout(resizeTimeout)
      resizeTimeout = setTimeout(() => onFitRequested(), 100)
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
      clearTimeout(resizeTimeout)
      terminal.dispose()
    }
  // Runs once per mount on purpose: the refs are stable, `fontSize` is only the
  // initial size (later changes go through terminal.options in
  // handleFontSizeChange), and everything that changes with the connection is
  // read through the Effect Events above.
  // eslint-disable-next-line react-hooks/exhaustive-deps -- created once per mount: the refs are stable, fontSize is only the initial size, and connection state is read through Effect Events
  }, [])

  // A new connection starts with the server's default PTY size; the only way it
  // learns ours is a resize message. The terminal used to be rebuilt on connect,
  // and the new ResizeObserver's initial callback sent that message as a side
  // effect. Now that the terminal outlives the connection, send it explicitly.
  useEffect(() => {
    if (isConnected) onFitRequested()
  }, [isConnected])

  // Stops the inactivity timers when the session ends or the terminal unmounts,
  // and only then. They used to be cleared in the xterm effect's cleanup, which
  // also runs when isConnected flips false -> true, so it wiped the timer onOpen
  // had just armed and an idle session never timed out (agent-os-z91e.27). The
  // disconnected render registers no cleanup, so connecting leaves the timer alone.
  useEffect(() => {
    if (!isConnected) return
    return () => clearInactivityTimers()
  }, [isConnected, clearInactivityTimers])

  useEffect(() => {
    const handleWindowResize = () => {
      onFitRequested()
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
