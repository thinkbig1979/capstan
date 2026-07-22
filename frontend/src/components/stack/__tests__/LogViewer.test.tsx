import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'

// Capture the message handler the component registers so the test can feed
// log lines through it, and spy on the WS `send` calls.
let capturedHandler: ((d: { container: string; timestamp: string; message: string }) => void) | null =
  null
const sendSpy = vi.fn()
const reconnectSpy = vi.fn()
// Mutable so individual tests can simulate a disconnected/reconnecting socket
// without needing a separate vi.mock per describe block.
let mockStatus: 'connected' | 'disconnected' | 'reconnecting' = 'connected'
let mockReconnectAttempts = 0

vi.mock('@/hooks/useWebSocket', () => ({
  useWebSocketJSON: (
    _url: string,
    handler: (d: { container: string; timestamp: string; message: string }) => void,
  ) => {
    capturedHandler = handler
    return {
      status: mockStatus,
      send: sendSpy,
      reconnect: reconnectSpy,
      reconnectAttempts: mockReconnectAttempts,
    }
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
  reconnectSpy.mockClear()
  mockStatus = 'connected'
  mockReconnectAttempts = 0
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

  it('clears the log buffer when the clear button is clicked', async () => {
    render(<LogViewer stackId="s1" />)
    await feed([line('alpha line')])
    expect(screen.getByText(/alpha line/)).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByTitle('Clear logs'))
    })

    expect(screen.queryByText(/alpha line/)).not.toBeInTheDocument()
    expect(screen.getByText('Waiting for logs...')).toBeInTheDocument()
  })

  it('toggles auto-scroll and records it in the store', async () => {
    render(<LogViewer stackId="s1" />)
    await act(async () => {
      fireEvent.click(screen.getByTitle('Auto-scroll enabled'))
    })
    expect(useUIStore.getState().logPrefs.autoScroll).toBe(false)
  })

  it('toggles timestamp visibility and hides the timestamp bracket when off', async () => {
    render(<LogViewer stackId="s1" />)
    await feed([line('alpha line')])
    expect(screen.getByText('[2024-01-01T00:00:00Z]')).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByTitle('Timestamps shown'))
    })

    expect(useUIStore.getState().logPrefs.showTimestamps).toBe(false)
    expect(screen.queryByText('[2024-01-01T00:00:00Z]')).not.toBeInTheDocument()
    // The message itself is still there — only the timestamp bracket is gone.
    expect(screen.getByText(/alpha line/)).toBeInTheDocument()
  })

  it('downloads the filtered logs as a text file with ANSI stripped', async () => {
    const createObjectURL = vi.fn((_blob: Blob | MediaSource) => 'blob:mock')
    const revokeObjectURL = vi.fn()
    const originalCreate = URL.createObjectURL
    const originalRevoke = URL.revokeObjectURL
    URL.createObjectURL = createObjectURL as typeof URL.createObjectURL
    URL.revokeObjectURL = revokeObjectURL
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    try {
      render(<LogViewer stackId="s1" />)
      await feed([line('alpha'), line(`${ESC}[31mboom${ESC}[0m`)])

      await act(async () => {
        fireEvent.click(screen.getByTitle('Download logs'))
      })

      expect(createObjectURL).toHaveBeenCalledTimes(1)
      const blob = createObjectURL.mock.calls[0][0] as Blob
      // jsdom's Blob has no .text()/.arrayBuffer(); FileReader is the
      // environment's supported way to read its contents back out.
      const text = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(reader.result as string)
        reader.onerror = reject
        reader.readAsText(blob)
      })
      expect(text).toContain('[web] alpha')
      expect(text).toContain('[web] boom')
      expect(text).not.toContain(ESC)
      expect(text).toMatch(/^\[2024-01-01T00:00:00Z\]/)
      expect(clickSpy).toHaveBeenCalledTimes(1)
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock')
    } finally {
      URL.createObjectURL = originalCreate
      URL.revokeObjectURL = originalRevoke
      clickSpy.mockRestore()
    }
  })

  it('pre-selects the initial container and sends it as the initial filter', async () => {
    render(<LogViewer stackId="s1" initialContainer="worker" />)
    expect(sendSpy).toHaveBeenCalledWith(JSON.stringify({ type: 'filter', containers: ['worker'] }))

    await feed([line('from worker', 'worker'), line('from api', 'api')])
    expect(screen.getByText(/from worker/)).toBeInTheDocument()
    expect(screen.queryByText(/from api/)).not.toBeInTheDocument()
  })

  it(
    'caps the buffer at MAX_LOG_BUFFER lines, dropping the oldest',
    { timeout: 30_000 },
    async () => {
      const { container } = render(<LogViewer stackId="s1" />)
      const many = Array.from({ length: 10005 }, (_, i) => line(`line ${i}`))
      await feed(many)

      // Direct DOM queries instead of RTL's getByText/queryByText: with 10,000
      // rendered rows, RTL's full-tree text-matching walk is the dominant cost
      // on top of the render itself. querySelector(All) + indexing is a single
      // native query with no per-node regex/normalization pass.
      const footer = container.querySelector('.justify-between.text-xs')
      expect(footer?.textContent).toMatch(/Showing 10000 logs/)

      const rows = container.querySelectorAll('[role="log"]')
      expect(rows.length).toBe(10000)
      // Oldest of the 10005 fed lines (0..4) were dropped, so the surviving
      // range is 5..10004 — oldest-first, newest-last.
      expect(rows[0].textContent).toMatch(/line 5$/)
      expect(rows[rows.length - 1].textContent).toMatch(/line 10004$/)
    }
  )

  it('shows a jump-to-latest pill counting lines that arrived while scrolled up, and resets on click', async () => {
    const { container } = render(<LogViewer stackId="s1" />)
    // scrolledUp/newCount tracking is independent of the auto-scroll toggle in
    // the component; turning it off here avoids racing this test's manual
    // scroll-metric overrides against the effect's own scrollToBottom() calls.
    await act(async () => {
      fireEvent.click(screen.getByTitle('Auto-scroll enabled'))
    })
    await feed([line('first')])

    const scrollEl = container.querySelector('.overflow-auto') as HTMLDivElement
    Object.defineProperty(scrollEl, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(scrollEl, 'clientHeight', { value: 100, configurable: true })
    Object.defineProperty(scrollEl, 'scrollTop', { value: 0, configurable: true, writable: true })

    await act(async () => {
      fireEvent.scroll(scrollEl)
    })
    expect(screen.getByText('Jump to latest')).toBeInTheDocument()

    await feed([line('second'), line('third')])
    expect(screen.getByText('2 new lines')).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByText('2 new lines'))
    })
    expect(screen.queryByText(/Jump to latest|new line/)).not.toBeInTheDocument()
  })

  it('shows the disconnected banner and reconnects on click', async () => {
    mockStatus = 'reconnecting'
    mockReconnectAttempts = 3
    render(<LogViewer stackId="s1" />)

    expect(screen.getByText(/Connection lost\. Reconnecting\.\.\. \(attempt 3\)/)).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Reconnect/ }))
    })
    expect(reconnectSpy).toHaveBeenCalledTimes(1)
  })

  it('shows a distinct empty-state message when no containers are running', () => {
    render(<LogViewer stackId="s1" hasRunningContainers={false} />)
    expect(
      screen.getByText('No containers are running. Start the stack to view logs.')
    ).toBeInTheDocument()
  })

  it('shows a distinct empty-state message when the errors-only filter matches nothing', async () => {
    render(<LogViewer stackId="s1" />)
    await feed([line('just an info line')])

    await act(async () => {
      fireEvent.click(screen.getByTitle('Show errors & warnings only'))
    })

    expect(screen.getByText('No errors or warnings match current filters')).toBeInTheDocument()
  })
})
