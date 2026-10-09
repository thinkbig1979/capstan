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
    // The second argument is the mutation's variables (undefined here).
    expect(onResult).toHaveBeenCalledWith(result, undefined)
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

// ─── rejected ActionResult (agent-os-ug4t) ────────────────────────────────────
//
// A FAILED action answers 5xx, so axios rejects and api.ts's interceptor hands
// onError {...body, status} — i.e. the ActionResult itself. classifyError reads
// data.error / data.message / err.message and an ActionResult carries none of
// them, so before this fix the cause was replaced by a bare status string.
//
// This is NOT the "failed outcome" case above: that one RESOLVES a 200 carrying
// outcome:'failed' and goes through onSuccess. It is green either way and
// cannot discriminate this change.

describe('useActionMutation — rejected ActionResult', () => {
  it('renders the reason from a 503 Docker-outage ActionResult, not the status string', async () => {
    const queryClient = makeClient()
    const dockerOutage = {
      outcome: 'failed',
      reason:
        'Docker daemon unreachable: the server started without a usable Docker connection. Check that the Docker socket is mounted and the daemon is running, then restart Capstan.',
      status: 503,
    }

    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockRejectedValue(dockerOutage),
        }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledWith(dockerOutage.reason)
  })

  it('renders the reason from a 500 failed ActionResult', async () => {
    const queryClient = makeClient()
    const failed = { outcome: 'failed', reason: 'failed to create network', status: 500 }

    const { result: hook } = renderHook(
      () => useActionMutation({ mutationFn: vi.fn().mockRejectedValue(failed) }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledWith('failed to create network')
  })

  it('falls back to the classified message when the ActionResult has an empty reason', async () => {
    const queryClient = makeClient()
    const emptyReason = { outcome: 'failed', reason: '', status: 500 }

    const { result: hook } = renderHook(
      () => useActionMutation({ mutationFn: vi.fn().mockRejectedValue(emptyReason) }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledWith('500: Something went wrong on the server')
  })

  // agent-os-5g8a. The single most likely regression from unifying the error
  // presenters, and nothing asserted it before: toastForResult maps OUTCOME to
  // toast LEVEL, so routing this branch through any presenter — all three of
  // which call toast.error — silently turns a partial into a flat failure. For
  // a `partial` that is a FALSE statement about what happened to the operator's
  // system: truth.Partial is minted at handlers/compose.go ("compose write
  // verification failed; rollback also failed") and handlers/env.go ("env file
  // created but DB not updated"), so a rollback that also failed would read as
  // "nothing happened".
  it('keeps the WARNING level for a rejected partial outcome', async () => {
    const queryClient = makeClient()
    const partial = {
      outcome: 'partial',
      reason: 'compose write verification failed; rollback also failed',
      status: 500,
    }

    const { result: hook } = renderHook(
      () => useActionMutation({ mutationFn: vi.fn().mockRejectedValue(partial) }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.warning).toHaveBeenCalledWith(partial.reason)
    expect(toast.error).not.toHaveBeenCalled()
  })

  // The same guard one outcome over: `no_change` must stay an INFO toast.
  it('keeps the INFO level for a rejected no_change outcome', async () => {
    const queryClient = makeClient()
    const noChange = { outcome: 'no_change', reason: 'Already up to date', status: 500 }

    const { result: hook } = renderHook(
      () => useActionMutation({ mutationFn: vi.fn().mockRejectedValue(noChange) }),
      { wrapper: wrapper(queryClient) },
    )

    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })

    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.info).toHaveBeenCalledWith('Already up to date')
    expect(toast.error).not.toHaveBeenCalled()
  })
})

// ─── errorTitle (agent-os-z91e.10) ────────────────────────────────────────────
//
// The wrapper does not know WHICH action failed, so it cannot title an error
// itself. `errorTitle` supplies that context, and only for a `failed` outcome:
// a rejected `partial` / `no_change` must keep its toast LEVEL.

describe('useActionMutation — errorTitle', () => {
  async function reject(rejection: unknown, errorTitle?: string) {
    const queryClient = makeClient()
    const { result: hook } = renderHook(
      () => useActionMutation({ mutationFn: vi.fn().mockRejectedValue(rejection), errorTitle }),
      { wrapper: wrapper(queryClient) },
    )
    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })
    await waitFor(() => expect(hook.current.isError).toBe(true))
  }

  it('titles a rejected failed ActionResult and carries the reason as the description', async () => {
    await reject({ outcome: 'failed', reason: 'Docker daemon unreachable', status: 503 }, 'Failed to start stack')
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error).toHaveBeenCalledWith('Failed to start stack', {
      description: 'Docker daemon unreachable',
    })
  })

  it('keeps the WARNING level for a rejected partial even when errorTitle is set', async () => {
    await reject({ outcome: 'partial', reason: 'rollback also failed', status: 500 }, 'Failed to start stack')
    expect(toast.warning).toHaveBeenCalledWith('rollback also failed')
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('keeps the INFO level for a rejected no_change even when errorTitle is set', async () => {
    await reject({ outcome: 'no_change', reason: 'Already up to date', status: 500 }, 'Failed to start stack')
    expect(toast.info).toHaveBeenCalledWith('Already up to date')
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('titles a non-ActionResult rejection and puts the classified cause in the description', async () => {
    await reject({ code: 'ERR_NETWORK', message: 'Network Error' }, 'Failed to start stack')
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error).toHaveBeenCalledWith('Failed to start stack', {
      description: 'Check your connection and try again',
    })
  })

  it('titles a failed ActionResult with an empty reason with the classified status', async () => {
    await reject({ outcome: 'failed', reason: '', status: 503 }, 'Failed to start stack')
    expect(toast.error).toHaveBeenCalledWith('Failed to start stack', {
      description: '503: Something went wrong on the server',
    })
  })

  it('without errorTitle a failed rejection is still the bare reason (unchanged)', async () => {
    await reject({ outcome: 'failed', reason: 'Docker daemon unreachable', status: 503 })
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error).toHaveBeenCalledWith('Docker daemon unreachable')
  })

  it('without errorTitle a non-ActionResult rejection is still the bare cause (unchanged)', async () => {
    await reject({ code: 'ERR_NETWORK', message: 'Network Error' })
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error).toHaveBeenCalledWith('Check your connection and try again')
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

// ─── silentWhen ──────────────────────────────────────────────────────────────

describe('useActionMutation — silentWhen', () => {
  class Cancelled extends Error {}

  function setup(silentWhen?: (err: unknown) => boolean) {
    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockRejectedValue(new Cancelled('declined')),
          errorTitle: 'Failed to delete',
          silentWhen,
        }),
      { wrapper: wrapper(makeClient()) },
    )
    return hook
  }

  it('toasts nothing for a rejection it names', async () => {
    const hook = setup((err) => err instanceof Cancelled)
    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })
    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('still toasts every rejection it does not name, and by default', async () => {
    const hook = setup((err) => !(err instanceof Cancelled))
    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })
    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledTimes(1)

    vi.clearAllMocks()
    const bare = setup()
    await act(async () => {
      bare.current.mutate(undefined as unknown as never)
    })
    await waitFor(() => expect(bare.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledTimes(1)
  })
})

// ─── Additions for stack and prune mutations (agent-os-cdsh) ──────────────────
//
// Three optional hooks the wrapper gained so useStackActions and PruneButton
// can move onto it without losing behaviour: a success title derived from the
// result, the mutation variables on onResult, and an onError callback. Plus
// the empty-reason guard on the SUCCESS path (the onError path already had it).

describe('useActionMutation — successTitle as a function of the result', () => {
  it('titles the success toast from the result', async () => {
    const { result: hook } = renderHook(
      () =>
        useActionMutation({
          mutationFn: vi.fn().mockResolvedValue({
            outcome: 'success',
            reason: 'pruned 3 image(s)',
            details: { deleted: ['a', 'b', 'c'] },
          } satisfies ActionResult),
          successTitle: (r) => `Pruned ${(r.details?.deleted as string[]).length} images`,
        }),
      { wrapper: wrapper(makeClient()) },
    )
    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })
    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(toast.success).toHaveBeenCalledWith('Pruned 3 images')
  })

  it('does not apply the title to no_change or partial, which keep their own text and level', async () => {
    const title = vi.fn(() => 'TITLE')
    for (const outcome of ['no_change', 'partial'] as const) {
      vi.clearAllMocks()
      const { result: hook } = renderHook(
        () =>
          useActionMutation({
            mutationFn: vi.fn().mockResolvedValue({ outcome, reason: `reason ${outcome}` }),
            successTitle: title,
          }),
        { wrapper: wrapper(makeClient()) },
      )
      await act(async () => {
        hook.current.mutate(undefined as unknown as never)
      })
      await waitFor(() => expect(hook.current.isSuccess).toBe(true))
      expect(toast.success).not.toHaveBeenCalled()
      expect((outcome === 'partial' ? toast.warning : toast.info)).toHaveBeenCalledWith(`reason ${outcome}`)
    }
  })
})

describe('useActionMutation — onResult receives the mutation variables', () => {
  it('passes the variables as the second argument, after the toast and the invalidations', async () => {
    const order: string[] = []
    const queryClient = makeClient()
    vi.spyOn(queryClient, 'invalidateQueries').mockImplementation(() => {
      order.push('invalidate')
      return Promise.resolve()
    })
    vi.mocked(toast.success).mockImplementation(() => {
      order.push('toast')
      return 1
    })
    const onResult = vi.fn(() => order.push('onResult'))
    const { result: hook } = renderHook(
      () =>
        useActionMutation<string>({
          mutationFn: vi.fn().mockResolvedValue({ outcome: 'success', reason: 'ok' }),
          invalidate: [['stacks']],
          onResult,
        }),
      { wrapper: wrapper(queryClient) },
    )
    await act(async () => {
      hook.current.mutate('stack-7')
    })
    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
    expect(onResult).toHaveBeenCalledWith({ outcome: 'success', reason: 'ok' }, 'stack-7')
    expect(order).toEqual(['toast', 'invalidate', 'onResult'])
  })
})

describe('useActionMutation — onError callback', () => {
  class Cancelled extends Error {}

  it('runs after the error toast, with the error and the variables', async () => {
    const order: string[] = []
    vi.mocked(toast.error).mockImplementation(() => {
      order.push('toast')
      return 1
    })
    const onError = vi.fn(() => order.push('onError'))
    const boom = { outcome: 'failed', reason: 'Docker is down' }
    const { result: hook } = renderHook(
      () => useActionMutation<string>({ mutationFn: vi.fn().mockRejectedValue(boom), onError }),
      { wrapper: wrapper(makeClient()) },
    )
    await act(async () => {
      hook.current.mutate('stack-7')
    })
    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(onError).toHaveBeenCalledWith(boom, 'stack-7')
    expect(order).toEqual(['toast', 'onError'])
  })

  it('still runs when silentWhen swallows the toast, so a caller can reset its own state', async () => {
    const onError = vi.fn()
    const { result: hook } = renderHook(
      () =>
        useActionMutation<string>({
          mutationFn: vi.fn().mockRejectedValue(new Cancelled('declined')),
          silentWhen: (err) => err instanceof Cancelled,
          onError,
        }),
      { wrapper: wrapper(makeClient()) },
    )
    await act(async () => {
      hook.current.mutate('stack-7')
    })
    await waitFor(() => expect(hook.current.isError).toBe(true))
    expect(toast.error).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledTimes(1)
  })
})

describe('useActionMutation — a 2xx failed result with no reason never toasts nothing (agent-os-cdsh)', () => {
  async function fire(data: ActionResult, errorTitle?: string) {
    const { result: hook } = renderHook(
      () => useActionMutation({ mutationFn: vi.fn().mockResolvedValue(data), errorTitle }),
      { wrapper: wrapper(makeClient()) },
    )
    await act(async () => {
      hook.current.mutate(undefined as unknown as never)
    })
    await waitFor(() => expect(hook.current.isSuccess).toBe(true))
  }

  it('shows the reason when there is one', async () => {
    await fire({ outcome: 'failed', reason: 'Container exited with code 1' })
    expect(toast.error).toHaveBeenCalledWith('Container exited with code 1')
  })

  it('falls back to the errorTitle, then to a generic sentence, when the reason is empty', async () => {
    await fire({ outcome: 'failed', reason: '' }, 'Failed to prune images')
    expect(toast.error).toHaveBeenCalledWith('Failed to prune images')

    vi.clearAllMocks()
    await fire({ outcome: 'failed', reason: '' })
    expect(toast.error).toHaveBeenCalledWith('Action failed')
  })
})
