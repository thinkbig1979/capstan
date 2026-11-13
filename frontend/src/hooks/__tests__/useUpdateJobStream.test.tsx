import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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
  /** Every socket constructed, in order: a redial shows up here, not in `instance`. */
  static urls: string[] = []
  url: string
  readyState = 0
  onopen: (() => void) | null = null
  onclose: ((e?: unknown) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: ((e: Event) => void) | null = null

  constructor(url: string) {
    this.url = url
    MockWebSocket.instance = this
    MockWebSocket.urls.push(url)
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
  MockWebSocket.urls = []
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

  it("keeps a 'partial' outcome on a done frame and on a snapshot (agent-os-qags.31)", async () => {
    // truth.Outcome has four members; handlers/updates.go forwards any
    // ActionResult outcome to SetOutcome, so partial must survive the parser.
    renderStream('job-1')
    await openSocket()

    frame({ type: 'snapshot', job: { ...job, status: 'success', outcome: 'partial', reason: '1 of 2 updated' } })
    expect(useUpdateJobStore.getState().jobs['job-1'].outcome).toBe('partial')

    frame({ type: 'done', status: 'success', outcome: 'partial', reason: '1 of 2 updated' })
    const stored = useUpdateJobStore.getState().jobs['job-1']
    expect(stored.outcome).toBe('partial')
    expect(stored.reason).toBe('1 of 2 updated')
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

// agent-os-z91e.28: WSClient ignores the late close event of a socket the
// caller closed, so handleClose never reports a run this hook closed itself (a
// skip flip, a job change or an unmount). The hook invalidates for it.
// Two-sided: a run that finished, or a hook that never streamed, does not.
describe('useUpdateJobStream — caller-closed incomplete run refreshes the updates queries', () => {
  const JOBS_KEY = ['resources', 'update-jobs']
  const UPDATES_KEY = ['resources', 'updates']

  function setup(initial: { id: string | null; enabled?: boolean }) {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    const spy = vi.spyOn(queryClient, 'invalidateQueries')
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    const hook = renderHook(
      (p: { id: string | null; enabled?: boolean }) =>
        useUpdateJobStream(p.id, { enabled: p.enabled }),
      { wrapper, initialProps: initial },
    )
    const keys = () => spy.mock.calls.map((c) => (c[0] as { queryKey: unknown }).queryKey)
    return { ...hook, keys }
  }

  it('invalidates when skip flips on mid-run', async () => {
    const { rerender, keys } = setup({ id: 'job-1' })
    await openSocket()
    expect(keys()).toEqual([])

    rerender({ id: 'job-1', enabled: false })

    expect(keys()).toEqual([JOBS_KEY, UPDATES_KEY])
  })

  it('invalidates when the hook unmounts mid-run', async () => {
    const { unmount, keys } = setup({ id: 'job-1' })
    await openSocket()

    unmount()

    expect(keys()).toEqual([JOBS_KEY, UPDATES_KEY])
  })

  it('invalidates for the run it leaves when the job id changes mid-run', async () => {
    const { rerender, keys } = setup({ id: 'job-1' })
    await openSocket()

    rerender({ id: 'job-2' })

    expect(keys()).toEqual([JOBS_KEY, UPDATES_KEY])
  })

  it('CONTROL: skip after a done frame invalidates nothing', async () => {
    const { rerender, keys } = setup({ id: 'job-1' })
    await openSocket()
    frame({ type: 'done', status: 'success', outcome: 'success' })

    rerender({ id: 'job-1', enabled: false })

    expect(keys()).toEqual([])
  })

  it('a done frame from the previous job does not hide an incomplete next one', async () => {
    const { rerender, unmount, keys } = setup({ id: 'job-1' })
    await openSocket()
    frame({ type: 'done', status: 'success', outcome: 'success' })

    rerender({ id: 'job-2' })
    expect(keys()).toEqual([])
    await waitFor(() => expect(MockWebSocket.instance!.url).toContain('job-2'))
    unmount()

    expect(keys()).toEqual([JOBS_KEY, UPDATES_KEY])
  })

  it('CONTROL: a hook that never streamed invalidates nothing on unmount', () => {
    const { unmount, keys } = setup({ id: null })

    unmount()

    expect(keys()).toEqual([])
  })
})

// agent-os-enxv: the server ends a job's stream after its terminal frame by
// closing the socket (gorilla's Close sends no close frame, so the browser
// sees 1006), and WSClient re-dials any such close. A finished job's open log
// re-dialled a ladder of sockets, and again on every tab focus, each one
// metered against the per-user cap. The hook now closes the stream itself on
// a terminal frame: done, or the "job not found" error frame.
describe('useUpdateJobStream — terminal frame ends the stream', () => {
  const finishedJob = {
    id: 'j1', targetType: 'stack', targetId: 's1', name: 'web', stackId: 's1',
    status: 'success', lines: [], createdAt: '2026-11-13T10:00:00Z',
  }

  /** Drop the live socket the way the server does, then give every re-dial
   *  route (ladder and focus recovery) time to fire. */
  function serverCloseThenWait() {
    vi.useFakeTimers()
    try {
      act(() => {
        MockWebSocket.instance!.onclose!({ code: 1006 })
      })
      act(() => { vi.advanceTimersByTime(60_000) })
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
      act(() => { document.dispatchEvent(new Event('visibilitychange')) })
      act(() => { vi.advanceTimersByTime(60_000) })
    } finally {
      vi.useRealTimers()
    }
  }

  it("does not redial after a done frame and the server's close (1006), nor on a visibilitychange", async () => {
    renderStream('j1')
    await openSocket()
    frame({ type: 'snapshot', job: finishedJob })
    frame({ type: 'done', status: 'success', outcome: 'success' })

    serverCloseThenWait()

    expect(MockWebSocket.urls).toHaveLength(1)
    expect(MockWebSocket.urls[0]).toContain('/ws/updates/jobs/j1')
    expect(useUpdateJobStore.getState().jobs['j1']?.status).toBe('success')
  })

  it("does not redial after a 'job not found' error frame", async () => {
    renderStream('j1')
    await openSocket()
    frame({ type: 'error', error: 'job not found' })

    serverCloseThenWait()

    expect(MockWebSocket.urls).toHaveLength(1)
  })

  it("still redials after a close with NO terminal frame (a live job's dropped stream re-attaches)", async () => {
    renderStream('j1')
    await openSocket()
    frame({ type: 'snapshot', job: { ...finishedJob, status: 'running' } })

    serverCloseThenWait()

    expect(MockWebSocket.urls.length).toBeGreaterThan(1)
    expect(MockWebSocket.urls.every(u => u.includes('/ws/updates/jobs/j1'))).toBe(true)
  })
})
