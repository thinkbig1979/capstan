import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import type { Stack, Container } from '@/types'
import type { UseWebSocketOptions } from '@/hooks/useWebSocket'

// Radix UI Select uses scrollIntoView internally; jsdom does not implement it.
window.HTMLElement.prototype.scrollIntoView = vi.fn()

// jsdom has no matchMedia, and xterm's theme/measurement code reaches for it
// on construction.
beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// Mirror the LogViewer.test.tsx pattern: capture the onMessage handler and the
// options object the component registers, so tests can simulate connection
// lifecycle events (onOpen/onClose/onError) and inbound binary frames without
// opening a real socket.
let capturedOnMessage: ((data: ArrayBuffer) => void) | null = null
let capturedOptions: UseWebSocketOptions | null = null
const sendSpy = vi.fn()
const disconnectSpy = vi.fn()
const reconnectSpy = vi.fn()

vi.mock('@/hooks/useWebSocket', () => ({
  useWebSocketBinary: (
    _path: string,
    onMessage: (data: ArrayBuffer) => void,
    options: UseWebSocketOptions,
  ) => {
    capturedOnMessage = onMessage
    capturedOptions = options
    return { send: sendSpy, disconnect: disconnectSpy, reconnect: reconnectSpy }
  },
}))

import { TerminalComponent } from '../Terminal'
import { toast } from 'sonner'
import { Terminal as XTerm } from '@xterm/xterm'

// Capture the real xterm instance the component constructs so tests can
// drive genuine selection state via its public select() API — the same
// path a mouse drag exercises through onSelectionChange.
let capturedTerminal: XTerm | null = null
const originalOpen = XTerm.prototype.open
function captureTerminal(instance: XTerm) {
  capturedTerminal = instance
}
beforeAll(() => {
  vi.spyOn(XTerm.prototype, 'open').mockImplementation(function (
    this: XTerm,
    ...args: Parameters<typeof originalOpen>
  ) {
    captureTerminal(this)
    return originalOpen.apply(this, args)
  })
})

function makeContainer(overrides: Partial<Container> = {}): Container {
  return {
    id: 'c1',
    name: 'web',
    image: 'nginx',
    state: 'running',
    status: 'Up 2 minutes',
    ports: [],
    ...overrides,
  }
}

function makeStack(containers: Container[] = [makeContainer()]): Stack {
  return {
    id: 'stack1',
    directory: '/srv/stack1',
    composeFile: 'docker-compose.yml',
    projectName: 'stack1',
    status: 'running',
    isGitRepo: false,
    gitDirty: false,
    gitAhead: 0,
    gitBehind: 0,
    containers,
  }
}

function connect() {
  act(() => {
    capturedOptions?.onOpen?.()
  })
}

beforeEach(() => {
  capturedOnMessage = null
  capturedOptions = null
  sendSpy.mockClear()
  disconnectSpy.mockClear()
  reconnectSpy.mockClear()
  vi.mocked(toast.success).mockClear()
  vi.mocked(toast.error).mockClear()
  vi.mocked(toast.info).mockClear()
  vi.mocked(toast.warning).mockClear()
  localStorage.clear()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('TerminalComponent — no running containers', () => {
  it('shows the empty state and hides the toolbar', () => {
    render(<TerminalComponent stack={makeStack([])} />)

    expect(screen.getByText('No running containers')).toBeInTheDocument()
    expect(screen.queryByText('Select container')).not.toBeInTheDocument()
  })
})

describe('TerminalComponent — container selection', () => {
  it('pre-selects the initialContainer prop', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    expect(screen.getByText('web')).toBeInTheDocument()
  })

  it('shows a placeholder select when no container is chosen yet', () => {
    render(<TerminalComponent stack={makeStack()} />)
    expect(screen.getByText('Select container')).toBeInTheDocument()
  })
})

describe('TerminalComponent — connection lifecycle', () => {
  it('shows connected state and success toast on open', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    expect(toast.success).toHaveBeenCalledWith('Terminal connected')
    expect(screen.getByText('Connected')).toBeInTheDocument()
  })

  it('clears connected state on close', async () => {
    // Quirk (verified via an XTerm.prototype.open spy): the terminal-creation
    // effect at Terminal.tsx:393 depends on `isConnected`, so every
    // connect/disconnect transition disposes and rebuilds the whole xterm
    // instance. The "Disconnected. Press Reconnect to continue." writeln at
    // Terminal.tsx:84 lands on the *old* instance a moment before that
    // instance is torn down by the rebuild, so the message never survives to
    // be observable — only the connection-state UI change is durable.
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()
    expect(screen.getByText('Connected')).toBeInTheDocument()

    await act(async () => {
      capturedOptions?.onClose?.()
      await new Promise((r) => setTimeout(r, 50))
    })

    expect(screen.queryByText('Connected')).not.toBeInTheDocument()
  })

  it('shows an error toast and clears connecting state on error', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)

    act(() => {
      capturedOptions?.onError?.(new Event('error'))
    })

    expect(toast.error).toHaveBeenCalledWith('Terminal connection error')
    expect(screen.queryByText('Connecting...')).not.toBeInTheDocument()
  })

  it('renders inbound binary frames into the terminal', async () => {
    const { container } = render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    const encoded = new TextEncoder().encode('HELLO-FROM-SHELL').buffer
    await act(async () => {
      capturedOnMessage?.(encoded)
      await new Promise((r) => setTimeout(r, 50))
    })

    expect(container.textContent).toContain('HELLO-FROM-SHELL')
  })
})

describe('TerminalComponent — disconnect / reconnect controls', () => {
  it('disconnects and toasts info when the disconnect button is clicked', async () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    await act(async () => {
      fireEvent.click(screen.getByTitle('Disconnect terminal'))
      await new Promise((r) => setTimeout(r, 50))
    })

    expect(disconnectSpy).toHaveBeenCalledTimes(1)
    expect(toast.info).toHaveBeenCalledWith('Terminal disconnected')
    expect(screen.queryByText('Connected')).not.toBeInTheDocument()
  })

  it('calls reconnect when the Reconnect button is clicked after a disconnect', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()
    fireEvent.click(screen.getByTitle('Disconnect terminal'))

    fireEvent.click(screen.getByRole('button', { name: /Reconnect/ }))
    expect(reconnectSpy).toHaveBeenCalledTimes(1)
  })
})

describe('TerminalComponent — switching containers', () => {
  it('disconnects the current session and selects the new container', async () => {
    const containers = [makeContainer({ id: 'c1', name: 'web' }), makeContainer({ id: 'c2', name: 'worker' })]
    render(<TerminalComponent stack={makeStack(containers)} initialContainer="c1" />)
    connect()

    fireEvent.click(screen.getByRole('combobox'))
    const option = await screen.findByRole('option', { name: 'worker' })
    await act(async () => {
      fireEvent.click(option)
      await new Promise((r) => setTimeout(r, 50))
    })

    expect(disconnectSpy).toHaveBeenCalledTimes(1)
    expect(screen.getByText('worker')).toBeInTheDocument()
    expect(screen.queryByText('Connected')).not.toBeInTheDocument()
  })

  it('does not disconnect when re-selecting the already-active container', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()
    disconnectSpy.mockClear()

    fireEvent.click(screen.getByRole('combobox'))
    // Re-clicking the current selection is a no-op per handleContainerChange's
    // value !== selectedContainer guard.
    expect(disconnectSpy).not.toHaveBeenCalled()
  })
})

describe('TerminalComponent — font size', () => {
  it('defaults to 14 and persists changes to localStorage, clamped to the max', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    expect(screen.getByTitle('Font size')).toHaveTextContent('14')

    for (let i = 0; i < 15; i++) {
      fireEvent.click(screen.getByTitle('Increase font size'))
    }

    expect(screen.getByTitle('Font size')).toHaveTextContent('24')
    expect(localStorage.getItem('terminal-font-size')).toBe('24')
    expect(screen.getByTitle('Increase font size')).toBeDisabled()
  })

  it('clamps decreases to the minimum', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    for (let i = 0; i < 15; i++) {
      fireEvent.click(screen.getByTitle('Decrease font size'))
    }

    expect(screen.getByTitle('Font size')).toHaveTextContent('10')
    expect(localStorage.getItem('terminal-font-size')).toBe('10')
    expect(screen.getByTitle('Decrease font size')).toBeDisabled()
  })

  it('restores a previously persisted font size on mount', () => {
    localStorage.setItem('terminal-font-size', '20')
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    expect(screen.getByTitle('Font size')).toHaveTextContent('20')
  })
})

describe('TerminalComponent — keyboard shortcuts', () => {
  it('opens the search bar on Ctrl+Shift+F while connected', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    expect(screen.queryByPlaceholderText('Find in terminal...')).not.toBeInTheDocument()

    fireEvent.keyDown(document, { key: 'f', ctrlKey: true, shiftKey: true })

    expect(screen.getByPlaceholderText('Find in terminal...')).toBeInTheDocument()
  })

  it('toggles the search bar closed via the toolbar search button', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    fireEvent.click(screen.getByTitle('Find (Ctrl+Shift+F)'))
    expect(screen.getByPlaceholderText('Find in terminal...')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('Find (Ctrl+Shift+F)'))
    expect(screen.queryByPlaceholderText('Find in terminal...')).not.toBeInTheDocument()
  })

  it('pastes clipboard text over the socket on Ctrl+Shift+V while connected', async () => {
    const readText = vi.fn().mockResolvedValue('pasted text')
    Object.assign(navigator, { clipboard: { readText, writeText: vi.fn() } })

    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    await act(async () => {
      fireEvent.keyDown(document, { key: 'v', ctrlKey: true, shiftKey: true })
      await Promise.resolve()
    })

    expect(readText).toHaveBeenCalled()
    expect(sendSpy).toHaveBeenCalled()
    const sentBuffer = sendSpy.mock.calls[0][0] as ArrayBuffer
    expect(new TextDecoder().decode(sentBuffer)).toBe('pasted text')
  })

  it('enables the Copy button once a selection exists and copies it to the clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { readText: vi.fn(), writeText } })

    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    await act(async () => {
      capturedOnMessage?.(new TextEncoder().encode('selectable line\r\n').buffer)
      await new Promise((r) => setTimeout(r, 50))
    })

    // Copy is disabled until xterm reports an active selection.
    expect(screen.getByTitle('Copy (Ctrl+Shift+C)')).toBeDisabled()

    act(() => {
      capturedTerminal?.select(0, 0, 'selectable line'.length)
    })

    expect(screen.getByTitle('Copy (Ctrl+Shift+C)')).not.toBeDisabled()

    await act(async () => {
      fireEvent.click(screen.getByTitle('Copy (Ctrl+Shift+C)'))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith('selectable line')
    expect(toast.success).toHaveBeenCalledWith('Copied to clipboard')
  })

  it('copies the current selection on Ctrl+Shift+C', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { readText: vi.fn(), writeText } })

    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    await act(async () => {
      capturedOnMessage?.(new TextEncoder().encode('another line\r\n').buffer)
      await new Promise((r) => setTimeout(r, 50))
    })

    act(() => {
      capturedTerminal?.select(0, 0, 'another line'.length)
    })

    await act(async () => {
      fireEvent.keyDown(document, { key: 'c', ctrlKey: true, shiftKey: true })
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith('another line')
  })
})

describe('TerminalComponent — keyboard hints', () => {
  it('shows the shortcuts panel by default while connected and dismisses it permanently', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    expect(screen.getByText('Keyboard Shortcuts')).toBeInTheDocument()

    fireEvent.click(screen.getByText("Don't show again"))

    expect(screen.queryByText('Keyboard Shortcuts')).not.toBeInTheDocument()
    expect(localStorage.getItem('terminal-hints-shown')).toBe('true')
  })

  it('does not show the hints panel again once dismissed and persisted', () => {
    localStorage.setItem('terminal-hints-shown', 'true')
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    connect()

    expect(screen.queryByText('Keyboard Shortcuts')).not.toBeInTheDocument()
  })
})

describe('TerminalComponent — session duration', () => {
  it('increments the active-for duration once per second while connected', () => {
    vi.useFakeTimers()
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    act(() => {
      capturedOptions?.onOpen?.()
    })

    expect(screen.getByText(/Active for 0:00/)).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(3000)
    })

    expect(screen.getByText(/Active for 0:03/)).toBeInTheDocument()
  })
})
