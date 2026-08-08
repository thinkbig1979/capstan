/**
 * MetricsPanel was at 0/75 statements, the largest of agent-os-c1gu's five. It is
 * imported only by StackDetail (lazily), which no test rendered.
 *
 * The WS TRANSPORT is mocked, not useMetricsBase — so the real hook does the real
 * accumulation and aggregation, and what is asserted below is the number a user
 * would actually read off the panel rather than a number this test computed and
 * handed to a stub.
 */
import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, act, within } from '@testing-library/react'
import type { MetricsMessage } from '@/hooks/useMetricsBase'

let capturedOnMessage: ((data: MetricsMessage) => void) | null = null
let capturedOptions: { onOpen?: () => void; onClose?: () => void } | null = null
let capturedPath: string | null = null
// Drives the branch selection in MetricsPanel: connecting -> skeletons,
// disconnected -> "Connection Lost", open -> the panel.
let wsState = { status: 'open' as 'connecting' | 'open' | 'disconnected', reconnectAttempts: 0 }

vi.mock('@/hooks/useWebSocket', () => ({
  useWebSocketJSON: (
    path: string,
    onMessage: (d: MetricsMessage) => void,
    options: { onOpen?: () => void; onClose?: () => void },
  ) => {
    capturedPath = path
    capturedOnMessage = onMessage
    capturedOptions = options
    return { lastMessage: null, send: vi.fn(), ...wsState }
  },
}))

import { MetricsPanel } from '../MetricsPanel'

beforeAll(() => {
  // recharts ResponsiveContainer needs a measurable box; ResizeObserver is not in jsdom.
  Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: () => ({ width: 100, height: 32, top: 0, left: 0, bottom: 32, right: 100, x: 0, y: 0, toJSON: () => {} }),
  })
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
})

function container(overrides: Partial<MetricsMessage['containers'][number]> = {}) {
  return {
    containerId: 'c1',
    name: 'web',
    cpuPercent: 0,
    memUsage: 0,
    memLimit: 0,
    memPercent: 0,
    netRx: 0,
    netTx: 0,
    blockRead: 0,
    blockWrite: 0,
    memSwap: 0,
    pids: 0,
    ...overrides,
  }
}

function frame(...containers: Array<Partial<MetricsMessage['containers'][number]>>): MetricsMessage {
  return { timestamp: '2026-08-08T12:00:00Z', containers: containers.map(container) }
}

/** Renders the panel, then pushes frames through the mocked socket. */
function renderWithFrames(...frames: MetricsMessage[]) {
  const result = render(<MetricsPanel stackId="stack-1" />)
  for (const f of frames) {
    act(() => {
      capturedOnMessage!(f)
    })
  }
  return result
}

beforeEach(() => {
  capturedOnMessage = null
  capturedOptions = null
  capturedPath = null
  wsState = { status: 'open', reconnectAttempts: 0 }
})

describe('MetricsPanel — connection states', () => {
  it('subscribes to the metrics socket for its stack', () => {
    render(<MetricsPanel stackId="stack-42" />)
    expect(capturedPath).toBe('/ws/metrics/stack-42')
  })

  it('shows skeletons while the socket is still connecting', () => {
    wsState = { status: 'connecting', reconnectAttempts: 0 }
    const { container: dom } = render(<MetricsPanel stackId="stack-1" />)

    expect(dom.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0)
    expect(screen.queryByText('Live Metrics')).not.toBeInTheDocument()
  })

  it('reports a lost connection when the socket is disconnected', () => {
    wsState = { status: 'disconnected', reconnectAttempts: 0 }
    render(<MetricsPanel stackId="stack-1" />)

    expect(screen.getByText('Connection Lost')).toBeInTheDocument()
    // No attempt count until one has actually been made.
    expect(screen.queryByText(/Reconnect attempt/)).not.toBeInTheDocument()
  })

  it('shows how many reconnects have been attempted', () => {
    wsState = { status: 'disconnected', reconnectAttempts: 3 }
    render(<MetricsPanel stackId="stack-1" />)

    expect(screen.getByText('Reconnect attempt 3/5')).toBeInTheDocument()
  })

  it('leaves the connecting skeletons once a frame arrives', () => {
    wsState = { status: 'connecting', reconnectAttempts: 0 }
    render(<MetricsPanel stackId="stack-1" />)
    expect(screen.queryByText('Live Metrics')).not.toBeInTheDocument()

    // A frame means the stream is live, whatever the socket status says — the
    // hook sets isConnected from the traffic itself.
    act(() => {
      capturedOnMessage!(frame({ cpuPercent: 5 }))
    })

    expect(screen.getByText('Live Metrics')).toBeInTheDocument()
  })

  it('says there are no running containers when the stream is empty', () => {
    render(<MetricsPanel stackId="stack-1" />)
    act(() => {
      capturedOptions!.onOpen!()
    })

    expect(screen.getByText('No Running Containers')).toBeInTheDocument()
  })
})

describe('MetricsPanel — summary tiles', () => {
  it('totals CPU across containers to one decimal place', () => {
    renderWithFrames(
      frame({ containerId: 'c1', cpuPercent: 12.34 }, { containerId: 'c2', cpuPercent: 5.61 }),
    )

    expect(screen.getByText('17.9%')).toBeInTheDocument()
  })

  it('shows memory used, the limit and the percentage', () => {
    renderWithFrames(
      frame({ memUsage: 512 * 1024 * 1024, memLimit: 1024 * 1024 * 1024, memPercent: 50 }),
    )

    expect(screen.getByText('512.00 MB')).toBeInTheDocument()
    expect(screen.getByText(/of 1\.00 GB · 50%/)).toBeInTheDocument()
  })

  it('sums network and disk rates over the latest sample of each container', () => {
    renderWithFrames(
      frame(
        { containerId: 'c1', netRx: 1000, netTx: 2000, blockRead: 3000, blockWrite: 4000 },
        { containerId: 'c2', netRx: 1000, netTx: 2000, blockRead: 3000, blockWrite: 4000 },
      ),
    )

    // Rendered as a rate, so the "/s" suffix is part of the contract.
    expect(screen.getAllByText('1.95 KB/s').length).toBeGreaterThan(0)
    expect(screen.getAllByText('3.91 KB/s').length).toBeGreaterThan(0)
    expect(screen.getAllByText('5.86 KB/s').length).toBeGreaterThan(0)
    expect(screen.getAllByText('7.81 KB/s').length).toBeGreaterThan(0)
  })

  it('labels all four summary tiles', () => {
    const { container: dom } = renderWithFrames(frame({}))

    // Scoped to the tile grid: "CPU", "Memory", "Network" and "Disk I/O" each
    // appear twice on this panel — once as a tile, once as a table column.
    const tiles = within(dom.querySelector<HTMLElement>('.grid-cols-2')!)
    expect(tiles.getByText('CPU')).toBeInTheDocument()
    expect(tiles.getByText('Memory')).toBeInTheDocument()
    expect(tiles.getByText('Network')).toBeInTheDocument()
    expect(tiles.getByText('Disk I/O')).toBeInTheDocument()
  })

  it('says the metrics are not stored, so nobody looks for history', () => {
    renderWithFrames(frame({}))
    expect(screen.getByText('Live since page opened, not stored')).toBeInTheDocument()
  })
})

describe('MetricsPanel — the per-container table', () => {
  it('lists each container with its CPU, memory and PID count', () => {
    renderWithFrames(
      frame(
        {
          containerId: 'c1',
          name: 'web',
          cpuPercent: 9.5,
          memUsage: 100 * 1024 * 1024,
          memLimit: 200 * 1024 * 1024,
          memPercent: 50,
          pids: 12,
        },
        { containerId: 'c2', name: 'db', cpuPercent: 1.25, pids: 7 },
      ),
    )

    expect(screen.getByText('web')).toBeInTheDocument()
    expect(screen.getByText('db')).toBeInTheDocument()
    expect(screen.getByText('9.5%')).toBeInTheDocument()
    expect(screen.getByText('1.3%')).toBeInTheDocument()
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(screen.getByText('7')).toBeInTheDocument()
    expect(screen.getByText('100.00 MB / 200.00 MB')).toBeInTheDocument()
  })

  it('names every column', () => {
    renderWithFrames(frame({}))

    // "PIDs" is unique to the header row, so it identifies the row to scope to —
    // four of the six names are shared with the summary tiles above.
    const headerRow = within(screen.getByText('PIDs').parentElement!)
    for (const header of ['Container', 'CPU', 'Memory', 'Network', 'Disk I/O', 'PIDs']) {
      expect(headerRow.getByText(header)).toBeInTheDocument()
    }
  })

  it('shows swap only when a container is actually swapping', () => {
    const { unmount } = renderWithFrames(frame({ memSwap: 0 }))
    expect(screen.queryByText(/swap/)).not.toBeInTheDocument()
    unmount()

    renderWithFrames(frame({ memSwap: 64 * 1024 * 1024 }))
    expect(screen.getByText(/· swap 64\.00 MB/)).toBeInTheDocument()
  })

  it('drops a container from the table once it stops reporting', () => {
    renderWithFrames(
      frame({ containerId: 'c1', name: 'web' }, { containerId: 'c2', name: 'db' }),
      frame({ containerId: 'c1', name: 'web' }),
    )

    expect(screen.getByText('web')).toBeInTheDocument()
    // A stopped container must not linger showing its last known numbers.
    expect(screen.queryByText('db')).not.toBeInTheDocument()
  })

  it('renders a sparkline per container plus one for the summary', () => {
    const { container: dom } = renderWithFrames(
      frame({ containerId: 'c1' }, { containerId: 'c2' }),
      frame({ containerId: 'c1' }, { containerId: 'c2' }),
    )

    // One summary chart + one per container.
    expect(dom.querySelectorAll('svg.recharts-surface').length).toBe(3)
  })
})

describe('MetricsPanel — threshold colouring', () => {
  /** The width style on the inner bar is what a user reads as "how full". */
  function memBarWidths(dom: HTMLElement): string[] {
    return Array.from(dom.querySelectorAll<HTMLElement>('.rounded-full > .rounded-full')).map(
      (el) => el.style.width,
    )
  }

  it('sizes the memory bar to the percentage', () => {
    const { container: dom } = renderWithFrames(frame({ memPercent: 42 }))
    expect(memBarWidths(dom)).toContain('42%')
  })

  it('clamps the bar at 100% when a container reports over its limit', () => {
    const { container: dom } = renderWithFrames(frame({ memPercent: 130 }))

    // Docker can report memPercent above 100; a 130%-wide div would overflow the row.
    expect(memBarWidths(dom)).toContain('100%')
    expect(memBarWidths(dom)).not.toContain('130%')
  })

  it('clamps a negative percentage at 0 rather than inverting the bar', () => {
    const { container: dom } = renderWithFrames(frame({ memPercent: -5 }))
    expect(memBarWidths(dom)).toContain('0%')
  })

  it('escalates the bar colour through the warning and critical thresholds', () => {
    function barClasses(dom: HTMLElement): string {
      return Array.from(dom.querySelectorAll<HTMLElement>('.rounded-full > .rounded-full'))
        .map((el) => el.className)
        .join(' ')
    }

    const healthy = renderWithFrames(frame({ memPercent: 10 }))
    expect(barClasses(healthy.container)).toContain('bg-success')
    healthy.unmount()

    const warning = renderWithFrames(frame({ memPercent: 65 }))
    expect(barClasses(warning.container)).toContain('bg-warning')
    warning.unmount()

    const critical = renderWithFrames(frame({ memPercent: 85 }))
    expect(barClasses(critical.container)).toContain('bg-destructive')
  })
})
