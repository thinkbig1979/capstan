import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'

// Capture the message handler the component registers so the test can feed
// log lines through it, and spy on the WS `send` calls.
let capturedHandler: ((d: { container: string; timestamp: string; message: string }) => void) | null =
  null
const sendSpy = vi.fn()

vi.mock('@/hooks/useWebSocket', () => ({
  useWebSocketJSON: (
    _url: string,
    handler: (d: { container: string; timestamp: string; message: string }) => void,
  ) => {
    capturedHandler = handler
    return { status: 'connected', send: sendSpy, reconnect: vi.fn(), reconnectAttempts: 0 }
  },
}))

import { LogViewer } from '../LogViewer'
import { useUIStore } from '@/stores/uiStore'

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

beforeEach(() => {
  capturedHandler = null
  sendSpy.mockClear()
  useUIStore.setState({
    logPrefs: {
      showTimestamps: true,
      autoScroll: true,
      wrap: true,
      timeRange: 'all',
      errorsOnly: false,
    },
  })
})

const ESC = '\x1b'

async function feed(lines: Array<{ container: string; timestamp: string; message: string }>) {
  await act(async () => {
    lines.forEach((l) => capturedHandler?.(l))
    // Component batches WS messages on a 50ms timer before committing to state.
    await new Promise((r) => setTimeout(r, 80))
  })
}

function line(message: string, container = 'web', timestamp = '2024-01-01T00:00:00Z') {
  return { container, timestamp, message }
}

describe('LogViewer', () => {
  it('shows a waiting message before any logs arrive', () => {
    render(<LogViewer stackId="s1" />)
    expect(screen.getByText('Waiting for logs...')).toBeInTheDocument()
  })

  it('renders ANSI-colored messages as readable text (escape codes stripped)', async () => {
    render(<LogViewer stackId="s1" />)
    await feed([line(`${ESC}[31mboom failed${ESC}[0m`)])
    // The visible text is the stripped content, not the raw escape sequence.
    expect(screen.getByText(/boom failed/)).toBeInTheDocument()
    expect(screen.queryByText(/\[31m/)).not.toBeInTheDocument()
  })

  it('filters to errors and warnings only when the toggle is on', async () => {
    render(<LogViewer stackId="s1" />)
    await feed([
      line('ERROR something broke'),
      line('just an info line'),
      line('WARN heads up'),
    ])
    expect(screen.getByText(/something broke/)).toBeInTheDocument()
    expect(screen.getByText(/just an info line/)).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByTitle('Show errors & warnings only'))
    })

    expect(screen.getByText(/something broke/)).toBeInTheDocument()
    expect(screen.getByText(/heads up/)).toBeInTheDocument()
    expect(screen.queryByText(/just an info line/)).not.toBeInTheDocument()
  })

  it('tracks every container across the batch and colors them distinctly', async () => {
    // Regression: the flush used to read the mutable batch ref inside the
    // state updaters, which raced with the buffer reset and dropped containers
    // from uniqueContainers — leaving every bracket the fallback color and the
    // container filter empty. Two distinct containers must get distinct colors.
    const { container } = render(<LogViewer stackId="s1" />)
    await feed([line('first', 'api'), line('second', 'worker')])

    const brackets = Array.from(
      container.querySelectorAll('[role="log"] span.font-medium'),
    )
    const colorByName: Record<string, string> = {}
    for (const b of brackets) {
      const name = (b.textContent || '').trim()
      const cls = b.getAttribute('class') || ''
      const m = cls.match(/text-\w+-\d+/)
      if (m && !colorByName[name]) colorByName[name] = m[0]
    }
    expect(colorByName['[api]']).toBeTruthy()
    expect(colorByName['[worker]']).toBeTruthy()
    // First-appearance round-robin: api -> red (index 0), worker -> orange (1).
    expect(colorByName['[api]']).not.toBe(colorByName['[worker]'])
    expect(colorByName['[api]']).toBe('text-red-600')
    expect(colorByName['[worker]']).toBe('text-orange-600')
  })

  it('persists the errors-only preference to the store', async () => {
    render(<LogViewer stackId="s1" />)
    await act(async () => {
      fireEvent.click(screen.getByTitle('Show errors & warnings only'))
    })
    expect(useUIStore.getState().logPrefs.errorsOnly).toBe(true)
  })

  it('toggles line wrapping and records it in the store', async () => {
    render(<LogViewer stackId="s1" />)
    await act(async () => {
      fireEvent.click(screen.getByTitle('Wrapping long lines'))
    })
    expect(useUIStore.getState().logPrefs.wrap).toBe(false)
  })

  it('filters logs by search term using stripped text', async () => {
    render(<LogViewer stackId="s1" />)
    await feed([line('alpha line'), line('bravo line')])

    await act(async () => {
      fireEvent.change(screen.getByPlaceholderText(/Search logs/), {
        target: { value: 'bravo' },
      })
    })

    // The match is highlighted, so "bravo" lives in its own <mark> node.
    expect(screen.getByText('bravo')).toBeInTheDocument()
    expect(screen.queryByText(/alpha/)).not.toBeInTheDocument()
  })

  it('focuses the search box when "/" is pressed', async () => {
    render(<LogViewer stackId="s1" />)
    const search = screen.getByPlaceholderText(/Search logs/)
    expect(search).not.toHaveFocus()
    await act(async () => {
      fireEvent.keyDown(window, { key: '/' })
    })
    expect(search).toHaveFocus()
  })
})
