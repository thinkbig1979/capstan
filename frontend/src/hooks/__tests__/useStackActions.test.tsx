import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { ActionResult } from '@/lib/action-result'

// ─── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
  },
}))

const mockStart   = vi.fn()
const mockStop    = vi.fn()
const mockRestart = vi.fn()
const mockDelete  = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    start:   (...args: unknown[]) => mockStart(...args),
    stop:    (...args: unknown[]) => mockStop(...args),
    restart: (...args: unknown[]) => mockRestart(...args),
    delete:  (...args: unknown[]) => mockDelete(...args),
  },
}))

import { toast } from 'sonner'
import { useStackActions } from '../useStackActions'

// ─── Helpers ─────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─── 2xx ActionResult responses ───────────────────────────────────────────────

describe('useStackActions — ActionResult: success outcome', () => {
  it('calls toast.success and invalidates all three query keys', async () => {
    const qc = makeClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    const result: ActionResult = { outcome: 'success', reason: 'Stack is running' }
    mockStart.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isSuccess).toBe(true))

    expect(toast.success).toHaveBeenCalledTimes(1)
    expect(toast.info).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()

    expect(spy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['stack'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['dashboard-stats'] })
  })
})

describe('useStackActions — ActionResult: no_change outcome', () => {
  it('calls toast.info, NOT toast.success', async () => {
    const qc = makeClient()
    const result: ActionResult = { outcome: 'no_change', reason: 'Stack already running' }
    mockStart.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isSuccess).toBe(true))

    expect(toast.info).toHaveBeenCalledWith('Stack already running')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('invalidates all query keys even for no_change', async () => {
    const qc = makeClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    const result: ActionResult = { outcome: 'no_change', reason: 'Already stopped' }
    mockStop.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })
    await act(async () => { hook.current.stop.mutate('my-stack') })
    await waitFor(() => expect(hook.current.stop.isSuccess).toBe(true))

    expect(spy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['stack'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['dashboard-stats'] })
  })
})

describe('useStackActions — ActionResult: failed outcome (2xx body)', () => {
  it('calls toast.error with the reason, NOT toast.success', async () => {
    const qc = makeClient()
    const result: ActionResult = { outcome: 'failed', reason: 'Container exited with code 1' }
    mockStart.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isSuccess).toBe(true))

    expect(toast.error).toHaveBeenCalledWith('Container exited with code 1')
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.info).not.toHaveBeenCalled()
  })

  it('still invalidates query keys so the UI reflects the failed state', async () => {
    const qc = makeClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    const result: ActionResult = { outcome: 'failed', reason: 'Failed to pull image' }
    mockRestart.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })
    await act(async () => { hook.current.restart.mutate('my-stack') })
    await waitFor(() => expect(hook.current.restart.isSuccess).toBe(true))

    expect(spy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['stack'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['dashboard-stats'] })
  })
})

describe('useStackActions — ActionResult: partial outcome', () => {
  it('calls toast.warning for partial success', async () => {
    const qc = makeClient()
    const result: ActionResult = { outcome: 'partial', reason: '1 of 2 services started' }
    mockStart.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isSuccess).toBe(true))

    expect(toast.warning).toHaveBeenCalledWith('1 of 2 services started')
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// ─── 500 ActionResult body in onError — the critical fix ─────────────────────
//
// When the backend returns HTTP 500 with an ActionResult body (outcome:'failed'),
// the axios interceptor rejects with `error.response?.data` directly, so the
// rejected value in onError IS the ActionResult. We must surface body.reason
// rather than a generic message.

describe('useStackActions — 500 ActionResult body surfaces reason in toast.error', () => {
  it('shows body.reason when the 500 response body is an ActionResult', async () => {
    const qc = makeClient()
    // Simulate axios interceptor: reject with the parsed response body directly.
    const errorBody: ActionResult = {
      outcome: 'failed',
      reason: 'docker: Error response from daemon — container exited with code 137',
    }
    mockStart.mockRejectedValue(errorBody)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isError).toBe(true))

    expect(toast.error).toHaveBeenCalledWith(
      'docker: Error response from daemon — container exited with code 137',
    )
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('falls back to classifyError message when error body is NOT an ActionResult', async () => {
    const qc = makeClient()
    // e.g. a 404 or network error — not an ActionResult body
    mockStop.mockRejectedValue({ code: 'ERR_NETWORK' })

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.stop.mutate('my-stack') })
    await waitFor(() => expect(hook.current.stop.isError).toBe(true))

    // classifyError maps ERR_NETWORK to this message
    expect(toast.error).toHaveBeenCalledWith('Check your connection and try again')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('does NOT call invalidateQueries when the mutation throws', async () => {
    const qc = makeClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    mockRestart.mockRejectedValue({ outcome: 'failed', reason: 'boom' })

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.restart.mutate('my-stack') })
    await waitFor(() => expect(hook.current.restart.isError).toBe(true))

    expect(spy).not.toHaveBeenCalled()
  })
})

// ─── delete returns a typed ActionResult (Action Truth Contract) ─────────────
//
// StacksHandler.Delete renders truth.Success("stack deleted") — delete now flows
// through toastForResult exactly like start/stop/restart, not a void fallback.

describe('useStackActions — delete routes through toastForResult', () => {
  it('shows toast.success titled "Stack deleted" on a success ActionResult', async () => {
    const qc = makeClient()
    const result: ActionResult = { outcome: 'success', reason: 'stack deleted' }
    mockDelete.mockResolvedValue(result)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.delete.mutate('my-stack') })
    await waitFor(() => expect(hook.current.delete.isSuccess).toBe(true))

    expect(toast.success).toHaveBeenCalledWith('Stack deleted')
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('surfaces the reason via toast.error when a delete is rejected (500 ActionResult body)', async () => {
    const qc = makeClient()
    const errorBody: ActionResult = { outcome: 'failed', reason: 'failed to run compose down' }
    mockDelete.mockRejectedValue(errorBody)

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.delete.mutate('my-stack') })
    await waitFor(() => expect(hook.current.delete.isError).toBe(true))

    expect(toast.error).toHaveBeenCalledWith('failed to run compose down')
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// ─── onSuccess / onResult callbacks ───────────────────────────────────────────

describe('useStackActions — onSuccess callback', () => {
  it('calls onSuccess after a successful mutation', async () => {
    const qc = makeClient()
    const onSuccess = vi.fn()
    const result: ActionResult = { outcome: 'success', reason: 'ok' }
    mockStop.mockResolvedValue(result)

    const { result: hook } = renderHook(
      () => useStackActions({ onSuccess }),
      { wrapper: wrapper(qc) },
    )

    await act(async () => { hook.current.stop.mutate('my-stack') })
    await waitFor(() => expect(hook.current.stop.isSuccess).toBe(true))

    expect(onSuccess).toHaveBeenCalledWith('stop', 'my-stack')
  })
})
