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
  // The shape handlers.jobSnapshotFrame carries (services.Job). This was
  // {id, status: 'running', lines} before agent-os-r4kf: 'running' is not a
  // services.Status and Go never omits the other fields, so the frame
  // validator rightly rejects that fixture.
  const job = {
    id: 'job-1',
    targetType: 'container',
    targetId: 'c1',
    name: 'web',
    stackId: 'stack-1',
    status: 'pulling',
    lines: [],
    createdAt: '2026-09-23T10:00:00Z',
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
    frame({ type: 'line', line: { ts: '2026-09-23T10:00:01Z', text: 'pulling…', stream: 'stdout' } })

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
    // 'updated' before agent-os-r4kf, which is not an outcome Go can emit.
    frame({ type: 'done', status: 'success', outcome: 'no_change', reason: 'Already up to date' })

    const stored = useUpdateJobStore.getState().jobs['job-1']
    expect(stored.status).toBe('success')
    expect(stored.outcome).toBe('no_change')
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

// agent-os-r4kf: useWebSocketJSON used to hand the consumer `JSON.parse(data)
// as T`, so a frame whose declared-string field was a number reached the store
// typed as a string. Every frame now passes the consumer's validator first.
describe('useUpdateJobStream — frame validation (agent-os-r4kf)', () => {
  const wireJob = {
    id: 'job-1',
    targetType: 'container',
    targetId: 'c1',
    name: 'web',
    stackId: '',
    status: 'pulling',
    lines: [],
    createdAt: '2026-09-23T10:00:00Z',
  }

  it('drops a line frame whose text is not a string instead of storing it', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job: wireJob })
    frame({ type: 'line', line: { ts: '2026-09-23T10:00:01Z', text: 42, stream: 'stdout' } })

    expect(useUpdateJobStore.getState().jobs['job-1'].lines).toHaveLength(0)
  })

  it('drops the pre-r4kf snapshot fixture, which no Go producer can send', async () => {
    renderStream('job-1')
    await openSocket()

    // The `frames` suite's job fixture before agent-os-r4kf: 'running' is not a
    // services.Status and the other services.Job fields are missing.
    frame({ type: 'snapshot', job: { id: 'job-1', status: 'running', lines: [] } })

    expect(useUpdateJobStore.getState().jobs['job-1']).toBeUndefined()
  })

  it("drops the pre-r4kf done fixture, whose outcome 'updated' Go never emits", async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job: wireJob })
    frame({ type: 'done', status: 'success', outcome: 'updated', reason: 'new digest' })

    const stored = useUpdateJobStore.getState().jobs['job-1']
    expect(stored.status).toBe('pulling')
    expect(stored.outcome).toBeUndefined()
  })

  it('still stores a well-formed line frame', async () => {
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job: wireJob })
    frame({ type: 'line', line: { ts: '2026-09-23T10:00:01Z', text: 'pulling', stream: 'stdout' } })

    expect(useUpdateJobStore.getState().jobs['job-1'].lines).toEqual([
      { ts: '2026-09-23T10:00:01Z', text: 'pulling', stream: 'stdout' },
    ])
  })
})
