import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { MetricsMessage } from '../useMetricsBase'

/**
 * useMetricsBase is the shared engine behind MetricsPanel and
 * useDashboardMetrics, and it was at 0/46 statements (agent-os-c1gu).
 * ContainerSparklineSort.test.tsx:7 imports it with `import type`, which is
 * erased at compile time and covered nothing — that line is not a test of it.
 *
 * Everything interesting here is history accumulation, trimming and the
 * disappearance of a stopped container, so the WS transport is mocked and the
 * captured onMessage callback is used to drive real metric frames through it.
 */

// Captured so tests can push frames, plus the options so the open/close
// callbacks the hook registers can be invoked.
let capturedOnMessage: ((data: MetricsMessage) => void) | null = null
let capturedOptions: { onOpen?: () => void; onClose?: () => void } | null = null
let capturedPath: string | null = null

vi.mock('../useWebSocket', () => ({
  useWebSocketJSON: (
    path: string,
    onMessage: (d: MetricsMessage) => void,
    options: { onOpen?: () => void; onClose?: () => void },
  ) => {
    capturedPath = path
    capturedOnMessage = onMessage
    capturedOptions = options
    return { lastMessage: null, status: 'open', send: vi.fn() }
  },
}))

import { useMetricsBase } from '../useMetricsBase'

/** One container's worth of a frame; every numeric field defaults so tests name only what they assert on. */
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

beforeEach(() => {
  capturedOnMessage = null
  capturedOptions = null
  capturedPath = null
})

describe('useMetricsBase', () => {
  it('subscribes to the path it was given', () => {
    renderHook(() => useMetricsBase('/ws/stacks/abc/metrics'))
    expect(capturedPath).toBe('/ws/stacks/abc/metrics')
  })

  it('starts empty and disconnected', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    expect(result.current.containers).toEqual([])
    expect(result.current.isConnected).toBe(false)
    expect(result.current.latestMetrics).toEqual({})
    expect(result.current.baseAggregates).toEqual({
      totalCpuPercent: 0,
      totalMemUsage: 0,
      totalMemLimit: 0,
      totalMemPercent: 0,
    })
  })

  it('records a frame and reports itself connected', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ containerId: 'c1', name: 'web', cpuPercent: 12.5, memUsage: 100 }))
    })

    expect(result.current.isConnected).toBe(true)
    expect(result.current.containers).toHaveLength(1)
    expect(result.current.containers[0]).toMatchObject({ containerId: 'c1', name: 'web' })
    expect(result.current.containers[0].metrics).toHaveLength(1)
    expect(result.current.latestMetrics.c1).toMatchObject({ cpuPercent: 12.5, memUsage: 100 })
  })

  it('accumulates history across frames rather than replacing it', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ cpuPercent: 1 }))
    })
    act(() => {
      capturedOnMessage!(frame({ cpuPercent: 2 }))
    })
    act(() => {
      capturedOnMessage!(frame({ cpuPercent: 3 }))
    })

    expect(result.current.containers[0].metrics.map((m) => m.cpuPercent)).toEqual([1, 2, 3])
    // latestMetrics is the tail of the history, not the whole of it.
    expect(result.current.latestMetrics.c1.cpuPercent).toBe(3)
  })

  it('trims history to historySize, keeping the newest samples', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics', { historySize: 3 }))

    for (const cpu of [1, 2, 3, 4, 5]) {
      act(() => {
        capturedOnMessage!(frame({ cpuPercent: cpu }))
      })
    }

    // The oldest two are dropped, not the newest two — a graph that scrolled the
    // wrong way would still have the right length.
    expect(result.current.containers[0].metrics.map((m) => m.cpuPercent)).toEqual([3, 4, 5])
  })

  it('defaults history to 60 samples', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    for (let i = 0; i < 65; i++) {
      act(() => {
        capturedOnMessage!(frame({ cpuPercent: i }))
      })
    }

    const cpus = result.current.containers[0].metrics.map((m) => m.cpuPercent)
    expect(cpus).toHaveLength(60)
    expect(cpus[0]).toBe(5)
    expect(cpus[59]).toBe(64)
  })

  it('drops a container that stops appearing in frames', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ containerId: 'c1', name: 'web' }, { containerId: 'c2', name: 'db' }))
    })
    expect(result.current.containers.map((c) => c.containerId)).toEqual(['c1', 'c2'])

    // c2 stopped: it must leave the list, not linger with a frozen last value.
    act(() => {
      capturedOnMessage!(frame({ containerId: 'c1', name: 'web' }))
    })

    expect(result.current.containers.map((c) => c.containerId)).toEqual(['c1'])
    expect(result.current.latestMetrics.c2).toBeUndefined()
  })

  it('keeps the name it first saw for a container', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ containerId: 'c1', name: 'web' }))
    })
    act(() => {
      capturedOnMessage!(frame({ containerId: 'c1', name: 'renamed-mid-stream' }))
    })

    expect(result.current.containers[0].name).toBe('web')
  })

  it('sums the latest sample of every container, not the whole history', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(
        frame(
          { containerId: 'c1', cpuPercent: 10, memUsage: 100, memLimit: 400 },
          { containerId: 'c2', cpuPercent: 5, memUsage: 100, memLimit: 400 },
        ),
      )
    })
    act(() => {
      capturedOnMessage!(
        frame(
          { containerId: 'c1', cpuPercent: 20, memUsage: 200, memLimit: 400 },
          { containerId: 'c2', cpuPercent: 30, memUsage: 200, memLimit: 400 },
        ),
      )
    })

    expect(result.current.baseAggregates).toEqual({
      totalCpuPercent: 50,
      totalMemUsage: 400,
      totalMemLimit: 800,
      totalMemPercent: 50,
    })
  })

  it('reports 0% total memory rather than NaN when no limit is set', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ memUsage: 100, memLimit: 0 }))
    })

    // A container with no memory limit divides by zero; the guard is what keeps
    // NaN% off the dashboard.
    expect(result.current.baseAggregates.totalMemPercent).toBe(0)
    expect(result.current.baseAggregates.totalMemUsage).toBe(100)
  })

  it('follows the socket open and close callbacks', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOptions!.onOpen!()
    })
    expect(result.current.isConnected).toBe(true)

    act(() => {
      capturedOptions!.onClose!()
    })
    expect(result.current.isConnected).toBe(false)
  })

  it('keeps the history it already collected when the socket closes', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ cpuPercent: 7 }))
    })
    act(() => {
      capturedOptions!.onClose!()
    })

    // A dropped connection should grey the panel out, not blank the graph.
    expect(result.current.isConnected).toBe(false)
    expect(result.current.containers[0].metrics.map((m) => m.cpuPercent)).toEqual([7])
  })

  it('handles an empty container list by emptying the view', () => {
    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    act(() => {
      capturedOnMessage!(frame({ containerId: 'c1' }))
    })
    act(() => {
      capturedOnMessage!({ timestamp: '2026-08-08T12:00:01Z', containers: [] })
    })

    expect(result.current.containers).toEqual([])
    expect(result.current.baseAggregates.totalCpuPercent).toBe(0)
    // The frame still counts as traffic, so the socket stays "connected".
    expect(result.current.isConnected).toBe(true)
  })

  it('does not throw when the server sends containers:null (agent-os-5scv)', () => {
    // Go marshals a nil slice as JSON null, not []. MetricsMessage.containers is
    // typed as a non-nullable array, so this cast is the only way to put the
    // real wire shape a buggy/older backend can still send through the type
    // system — the whole point of this test is that the type cannot be
    // trusted at the wire boundary.
    const nullContainersMessage = {
      timestamp: '2026-08-08T12:00:00Z',
      containers: null,
    } as unknown as MetricsMessage

    const { result } = renderHook(() => useMetricsBase('/ws/metrics'))

    expect(() => {
      act(() => {
        capturedOnMessage!(nullContainersMessage)
      })
    }).not.toThrow()

    // A null frame reports no containers, same as an empty-array frame — it
    // must not leave stale history behind or crash the updater.
    expect(result.current.containers).toEqual([])
    expect(result.current.isConnected).toBe(true)
  })
})
