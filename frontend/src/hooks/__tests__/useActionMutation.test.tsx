import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useActionMutation } from '../useActionMutation'
import type { ActionResult } from '@/lib/action-result'

// Mock sonner so we can assert which toast method was called.
vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
  },
}))

import { toast } from 'sonner'

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

// ─── Success outcome ──────────────────────────────────────────────────────────

describe('useActionMutation — success outcome', () => {
  it('calls toast.success and invalidates provided query keys', async () => {
    const queryClient = makeClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const result: ActionResult = { outcome: 'success', reason: 'Stack started' }
    const mutationFn = vi.fn().mockResolvedValue(result)

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn,
          invalidate: [['stacks'], ['dashboard-stats']],
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isSuccess).toBe(true))

    expect(toast.success).toHaveBeenCalledWith('Stack started')
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['dashboard-stats'] })
  })

  it('uses successTitle override in the toast', async () => {
    const queryClient = makeClient()
    const result: ActionResult = { outcome: 'success', reason: 'Done' }

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockResolvedValue(result),
          successTitle: 'Update applied',
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(toast.success).toHaveBeenCalledWith('Update applied')
  })

  it('calls onResult with the typed data', async () => {
    const queryClient = makeClient()
    const result: ActionResult = { outcome: 'success', reason: 'ok' }
    const onResult = vi.fn()

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockResolvedValue(result),
          onResult,
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(onResult).toHaveBeenCalledWith(result)
  })
})

// ─── no_change outcome ────────────────────────────────────────────────────────

describe('useActionMutation — no_change outcome', () => {
  it('calls toast.info and still invalidates keys', async () => {
    const queryClient = makeClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const result: ActionResult = { outcome: 'no_change', reason: 'Image already up to date' }

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockResolvedValue(result),
          invalidate: [['resources', 'updates']],
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(toast.info).toHaveBeenCalledWith('Image already up to date')
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['resources', 'updates'] })
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// ─── failed outcome ───────────────────────────────────────────────────────────

describe('useActionMutation — failed outcome', () => {
  it('calls toast.error with the reason', async () => {
    const queryClient = makeClient()
    const result: ActionResult = { outcome: 'failed', reason: 'Pull failed: manifest unknown' }

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockResolvedValue(result),
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(toast.error).toHaveBeenCalledWith('Pull failed: manifest unknown')
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// ─── partial outcome ──────────────────────────────────────────────────────────

describe('useActionMutation — partial outcome', () => {
  it('calls toast.warning', async () => {
    const queryClient = makeClient()
    const result: ActionResult = { outcome: 'partial', reason: '1 of 2 services updated' }

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockResolvedValue(result),
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(toast.warning).toHaveBeenCalledWith('1 of 2 services updated')
  })
})

// ─── network/throw error ──────────────────────────────────────────────────────

describe('useActionMutation — mutationFn throws', () => {
  it('calls toast.error with the classified message', async () => {
    const queryClient = makeClient()
    const networkError = { code: 'ERR_NETWORK', message: 'Network Error' }

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockRejectedValue(networkError),
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledWith('Check your connection and try again')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('does not invalidate keys when mutation throws', async () => {
    const queryClient = makeClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockRejectedValue(new Error('boom')),
          invalidate: [['stacks']],
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(invalidateSpy).not.toHaveBeenCalled()
  })
})
