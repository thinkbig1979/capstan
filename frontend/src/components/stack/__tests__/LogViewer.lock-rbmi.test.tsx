import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import type { UseWebSocketJSONOptions } from '@/hooks/useWebSocket'

// agent-os-rbmi: log lines arrive masked or raw by the unlock the socket
// carries, so a change of token drops what is on screen and reopens the
// stream under the new one.

type Line = { container: string; timestamp: string; message: string }
let capturedHandler: ((d: Line) => void) | null = null
let capturedOptions: UseWebSocketJSONOptions<Line> | null = null
const reconnectSpy = vi.fn()

vi.mock('@/hooks/useWebSocket', () => ({
  useWebSocketJSON: (_url: string, handler: (d: Line) => void, options: UseWebSocketJSONOptions<Line>) => {
    capturedHandler = handler
    capturedOptions = options
    return { status: 'connected', send: vi.fn(), reconnect: reconnectSpy, reconnectAttempts: 0 }
  },
}))

import { LogViewer } from '../LogViewer'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false, media: query, onchange: null,
      addListener: () => {}, removeListener: () => {},
      addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
    }),
  })
})

beforeEach(() => {
  capturedHandler = null
  capturedOptions = null
  reconnectSpy.mockClear()
  useEnvUnlockStore.getState().lock()
})

afterEach(() => {
  useEnvUnlockStore.getState().lock()
})

async function feed(message: string) {
  await act(async () => {
    capturedHandler?.({ container: 'web', timestamp: '2024-01-01T00:00:00Z', message })
    await new Promise((r) => setTimeout(r, 80))
  })
}

describe('LogViewer — env lock', () => {
  it('sends the unlock token with the stream', () => {
    render(<LogViewer stackId="s1" hasRunningContainers />)
    expect(capturedOptions?.sendUnlockToken).toBe(true)
  })

  it('CONTROL: no token change, no reconnect', async () => {
    render(<LogViewer stackId="s1" hasRunningContainers />)
    await feed('secret is hunter2')
    expect(reconnectSpy).not.toHaveBeenCalled()
    expect(screen.getByText(/hunter2/)).toBeInTheDocument()
  })

  it('drops the lines shown raw and reopens the stream when the session locks', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok-1'))
    render(<LogViewer stackId="s1" hasRunningContainers />)
    await feed('secret is hunter2')
    expect(screen.getByText(/hunter2/)).toBeInTheDocument()

    act(() => useEnvUnlockStore.getState().lock())

    expect(screen.queryByText(/hunter2/)).not.toBeInTheDocument()
    expect(reconnectSpy).toHaveBeenCalledTimes(1)
  })

  it('reopens the stream on unlock, so the lines come back raw', async () => {
    render(<LogViewer stackId="s1" hasRunningContainers />)
    await feed('secret is ***')

    act(() => useEnvUnlockStore.getState().unlock('tok-1'))

    expect(screen.queryByText(/secret is/)).not.toBeInTheDocument()
    expect(reconnectSpy).toHaveBeenCalledTimes(1)
  })
})
