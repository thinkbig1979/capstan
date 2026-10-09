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

    // R7/R8: the action is the title, the server-authored reason the description.
    expect(toast.error).toHaveBeenCalledWith('Failed to start stack', {
      description: 'docker: Error response from daemon — container exited with code 137',
    })
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('falls back to classifyError message when error body is NOT an ActionResult', async () => {
    const qc = makeClient()
    // e.g. a 404 or network error — not an ActionResult body
    mockStop.mockRejectedValue({ code: 'ERR_NETWORK' })

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })

    await act(async () => { hook.current.stop.mutate('my-stack') })
    await waitFor(() => expect(hook.current.stop.isError).toBe(true))

    // classifyError maps ERR_NETWORK to this message, under the action's title
    expect(toast.error).toHaveBeenCalledWith('Failed to stop stack', {
      description: 'Check your connection and try again',
    })
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

    expect(toast.error).toHaveBeenCalledWith('Failed to delete stack', {
      description: 'failed to run compose down',
    })
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// ─── onSuccess / onResult callbacks ───────────────────────────────────────────

// ─── delete: 428 STACK_DELETE_COLLATERAL re-confirmation (agent-os-7et) ──────

describe('useStackActions — delete re-confirms on 428 STACK_DELETE_COLLATERAL', () => {
  const collateralError = {
    code: 'STACK_DELETE_COLLATERAL',
    message: 'Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed',
    details: { directory: '/opt/stacks/my-stack', collateral: ['data', '.git'] },
  }

  it('retries with confirmCollateral=true and succeeds when the user confirms', async () => {
    const qc = makeClient()
    mockDelete
      .mockRejectedValueOnce(collateralError)
      .mockResolvedValueOnce({ outcome: 'success', reason: 'stack deleted' })
    const confirmCollateral = vi.fn().mockResolvedValue(true)

    const { result: hook } = renderHook(
      () => useStackActions({ confirmCollateral }),
      { wrapper: wrapper(qc) },
    )

    await act(async () => { hook.current.delete.mutate('my-stack') })
    await waitFor(() => expect(hook.current.delete.isSuccess).toBe(true))

    expect(mockDelete).toHaveBeenNthCalledWith(1, 'my-stack')
    expect(mockDelete).toHaveBeenNthCalledWith(2, 'my-stack', true)
    expect(toast.success).toHaveBeenCalledWith('Stack deleted')
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('does not retry and does not toast an error when the user declines', async () => {
    const qc = makeClient()
    mockDelete.mockRejectedValueOnce(collateralError)
    const confirmCollateral = vi.fn().mockResolvedValue(false)
    const onError = vi.fn()

    const { result: hook } = renderHook(
      () => useStackActions({ confirmCollateral, onError }),
      { wrapper: wrapper(qc) },
    )

    await act(async () => { hook.current.delete.mutate('my-stack') })
    await waitFor(() => expect(hook.current.delete.isError).toBe(true))

    // Exactly the first attempt — a decline must never trigger the retry.
    expect(mockDelete).toHaveBeenCalledTimes(1)
    expect(toast.error).not.toHaveBeenCalled()
    // The caller's onError still fires so it can reset local "deleting" state.
    expect(onError).toHaveBeenCalledWith('delete', 'my-stack')
  })
})

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

// ─── useActionMutation parity (agent-os-cdsh) ─────────────────────────────────
//
// The four mutations moved from a raw useMutation onto useActionMutation.
// These rows pin what the move had to keep.

describe('useStackActions — on useActionMutation', () => {
  it('passes (action, id) to onSuccess and onResult, in that order, after the invalidations', async () => {
    const qc = makeClient()
    const order: string[] = []
    vi.spyOn(qc, 'invalidateQueries').mockImplementation(() => {
      order.push('invalidate')
      return Promise.resolve()
    })
    const onSuccess = vi.fn(() => order.push('onSuccess'))
    const onResult = vi.fn(() => order.push('onResult'))
    mockRestart.mockResolvedValue({ outcome: 'success', reason: 'ok' })

    const { result: hook } = renderHook(
      () => useStackActions({ onSuccess, onResult }),
      { wrapper: wrapper(qc) },
    )
    await act(async () => { hook.current.restart.mutate('my-stack') })
    await waitFor(() => expect(hook.current.restart.isSuccess).toBe(true))

    expect(onSuccess).toHaveBeenCalledWith('restart', 'my-stack')
    expect(onResult).toHaveBeenCalledWith('restart', 'my-stack')
    expect(order).toEqual(['invalidate', 'invalidate', 'invalidate', 'onSuccess', 'onResult'])
  })

  it('calls onError(action, id) after a rejected failed result', async () => {
    const qc = makeClient()
    const onError = vi.fn()
    mockStart.mockRejectedValue({ outcome: 'failed', reason: 'boom' })

    const { result: hook } = renderHook(() => useStackActions({ onError }), { wrapper: wrapper(qc) })
    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isError).toBe(true))

    expect(onError).toHaveBeenCalledTimes(1)
    expect(onError).toHaveBeenCalledWith('start', 'my-stack')
  })

  it('keeps a rejected partial at warning level, not an error', async () => {
    const qc = makeClient()
    mockStop.mockRejectedValue({ outcome: 'partial', reason: '1 of 2 services stopped' })

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })
    await act(async () => { hook.current.stop.mutate('my-stack') })
    await waitFor(() => expect(hook.current.stop.isError).toBe(true))

    expect(toast.warning).toHaveBeenCalledWith('1 of 2 services stopped')
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('never toasts an empty error for a 2xx failed result with no reason', async () => {
    const qc = makeClient()
    mockStart.mockResolvedValue({ outcome: 'failed', reason: '' })

    const { result: hook } = renderHook(() => useStackActions(), { wrapper: wrapper(qc) })
    await act(async () => { hook.current.start.mutate('my-stack') })
    await waitFor(() => expect(hook.current.start.isSuccess).toBe(true))

    expect(toast.error).toHaveBeenCalledWith('Failed to start stack')
  })
})
