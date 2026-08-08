import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useUpdateJobStream } from '../useUpdateJobStream'
import { useUpdateJobStore } from '@/stores/updateJobStore'
import { useAuthStore } from '@/stores/authStore'

/**
 * UpdateJobStatusCell.test.tsx:20 stubs this hook out, so nothing exercised it
 * (0/27 statements, agent-os-m1mu criterion 5: stub in the parent test OR test
 * directly, never neither).
 *
 * The WebSocket itself is faked at the global level rather than by mocking
 * useWebSocketJSON, so the real frame routing, the real store writes and the
 * real skip/path wiring all run.
 */

class MockWebSocket {
  static instance: MockWebSocket | null = null
  url: string
  readyState = 0
  onopen: (() => void) | null = null
  onclose: ((e?: unknown) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e: Event) => void) | null = null

  constructor(url: string) {
    this.url = url
    MockWebSocket.instance = this
  }

  send() {}
  close() {
    this.readyState = 3
  }
}

let originalWebSocket: typeof WebSocket

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

const renderStream = (jobId: string | null, opts?: { enabled?: boolean }) =>
  renderHook(() => useUpdateJobStream(jobId, opts), { wrapper: createWrapper() })

/** Deliver a frame the way the server would. */
const frame = (payload: unknown) =>
  act(() => {
    MockWebSocket.instance!.onmessage!({ data: JSON.stringify(payload) })
  })

const openSocket = async () => {
  await waitFor(() => expect(MockWebSocket.instance).not.toBeNull())
  act(() => {
    MockWebSocket.instance!.onopen!()
  })
}

beforeEach(() => {
  originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  MockWebSocket.instance = null
  useAuthStore.setState({
    token: null,
    user: null,
    isAuthenticated: false,
    authDisabled: true,
    needsSetup: false,
  })
  useUpdateJobStore.setState(useUpdateJobStore.getInitialState())
})

afterEach(() => {
  globalThis.WebSocket = originalWebSocket
})

describe('useUpdateJobStream — connecting', () => {
  it('does not connect without a job id', () => {
    const { result } = renderStream(null)

    expect(MockWebSocket.instance).toBeNull()
    expect(result.current.connected).toBe(false)
  })

  it('does not connect when explicitly disabled', () => {
    renderStream('job-1', { enabled: false })

    expect(MockWebSocket.instance).toBeNull()
  })

  it('connects to the job path once a job id is given', async () => {
    renderStream('job-1')

    await waitFor(() => expect(MockWebSocket.instance).not.toBeNull())
    expect(MockWebSocket.instance!.url).toContain('/ws/updates/jobs/job-1')
  })

  it('reports connected only once the socket opens', async () => {
    const { result } = renderStream('job-1')

    expect(result.current.connected).toBe(false)
    await openSocket()

    await waitFor(() => expect(result.current.connected).toBe(true))
  })

  it('connects when a job id arrives after mounting with none', async () => {
    // This is the behaviour the '_noop' sentinel path used to fake, before
    // skip became a real dependency of the connect effect (agent-os-9d5e).
    const { rerender } = renderHook(({ id }: { id: string | null }) => useUpdateJobStream(id), {
      wrapper: createWrapper(),
      initialProps: { id: null as string | null },
    })

    expect(MockWebSocket.instance).toBeNull()

    rerender({ id: 'job-2' })

    await waitFor(() => expect(MockWebSocket.instance).not.toBeNull())
    expect(MockWebSocket.instance!.url).toContain('/ws/updates/jobs/job-2')
  })
})

describe('useUpdateJobStream — frames', () => {
  const job = {
    id: 'job-1',
    status: 'running' as const,
    lines: [],
  }

  it('upserts the job from a snapshot frame', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })

    expect(useUpdateJobStore.getState().jobs['job-1']).toBeDefined()
  })

  it('appends output lines', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })
    frame({ type: 'line', line: { text: 'pulling…', stream: 'stdout' } })

    expect(useUpdateJobStore.getState().jobs['job-1'].lines).toHaveLength(1)
  })

  it('applies a status frame', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })
    frame({ type: 'status', status: 'error', error: 'pull failed' })

    expect(useUpdateJobStore.getState().jobs['job-1'].status).toBe('error')
  })

  it('applies a done frame, including the typed outcome', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })
    frame({ type: 'done', status: 'success', outcome: 'updated', reason: 'new digest' })

    const stored = useUpdateJobStore.getState().jobs['job-1']
    expect(stored.status).toBe('success')
    expect(stored.outcome).toBe('updated')
  })

  it('leaves the outcome alone when a done frame carries none', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })
    frame({ type: 'done', status: 'success' })

    expect(useUpdateJobStore.getState().jobs['job-1'].outcome).toBeUndefined()
  })

  it('marks the job as errored on a server error frame', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })
    frame({ type: 'error', error: 'job evicted' })

    expect(useUpdateJobStore.getState().jobs['job-1'].status).toBe('error')
  })

  it('ignores an unrecognised frame type rather than throwing', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job })
    expect(() => frame({ type: 'something-new' })).not.toThrow()
  })

  it('ignores malformed JSON rather than throwing', async () => {
    renderStream('job-1')
    await openSocket()

    expect(() =>
      act(() => {
        MockWebSocket.instance!.onmessage!({ data: 'not json' })
      }),
    ).not.toThrow()
  })
})
