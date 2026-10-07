import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { toast } from 'sonner'

const mockStart = vi.fn()
const mockStop = vi.fn()
const mockToastInvalid = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    start: (...a: unknown[]) => mockStart(...a),
    stop: (...a: unknown[]) => mockStop(...a),
    restart: vi.fn(),
    pull: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/lib/error-handler', () => ({
  toastInvalid: (...a: unknown[]) => mockToastInvalid(...a),
}))

import { useSidebarSelection } from '../useSidebarSelection'
import type { Stack } from '@/types'

/**
 * agent-os-z91e.16. runBulk counted a stack as OK when its call FULFILLED. A
 * stack start/stop/restart that leaves one service down answers 207 with a
 * `partial` ActionResult (truth.ActionResult.HTTPStatus), which axios resolves,
 * so a partial was counted as a success and a batch of them toasted "Started 2
 * stacks". The outcome has to be read.
 */

const result = (outcome: 'success' | 'no_change' | 'partial' | 'failed') => ({
  outcome,
  reason: `reason ${outcome}`,
})

function renderSelection(ids: string[]) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  const stacks = ids.map((id) => ({ id }) as Stack)
  const view = renderHook(() => useSidebarSelection(stacks), { wrapper })
  for (const id of ids) act(() => view.result.current.toggleSelected(id))
  return view
}

async function bulkStart(ids: string[], outcomes: unknown[]) {
  ids.forEach((_, i) => {
    const o = outcomes[i]
    if (o instanceof Error) mockStart.mockRejectedValueOnce(o)
    else mockStart.mockResolvedValueOnce(o)
  })
  const view = renderSelection(ids)
  await act(async () => {
    await view.result.current.runBulk('start')
  })
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useSidebarSelection.runBulk reads each ActionResult outcome (agent-os-z91e.16)', () => {
  it('a partial in a mixed batch toasts the warning, not success', async () => {
    await bulkStart(['a', 'b'], [result('success'), result('partial')])

    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.warning).toHaveBeenCalledWith('Started 1, 1 failed or only partly done')
    expect(mockToastInvalid).not.toHaveBeenCalled()
  })

  it('a batch of only partials is not success and does not say "Failed"', async () => {
    await bulkStart(['a', 'b'], [result('partial'), result('partial')])

    expect(toast.success).not.toHaveBeenCalled()
    expect(mockToastInvalid).toHaveBeenCalledWith('Could not fully start 2 stacks')
    expect(toast.warning).not.toHaveBeenCalled()
  })

  it('a single partial reads in the singular', async () => {
    await bulkStart(['a'], [result('partial')])

    expect(toast.success).not.toHaveBeenCalled()
    expect(mockToastInvalid).toHaveBeenCalledWith('Could not fully start 1 stack')
  })

  it('a fulfilled failed outcome counts as not OK', async () => {
    await bulkStart(['a', 'b'], [result('success'), result('failed')])

    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.warning).toHaveBeenCalledWith('Started 1, 1 failed or only partly done')
  })

  it('every outcome success still toasts success', async () => {
    await bulkStart(['a', 'b'], [result('success'), result('success')])

    expect(toast.success).toHaveBeenCalledWith('Started 2 stacks')
    expect(toast.warning).not.toHaveBeenCalled()
    expect(mockToastInvalid).not.toHaveBeenCalled()
  })

  it('no_change counts as OK (already in the requested state)', async () => {
    await bulkStart(['a', 'b'], [result('success'), result('no_change')])

    expect(toast.success).toHaveBeenCalledWith('Started 2 stacks')
    expect(toast.warning).not.toHaveBeenCalled()
  })

  it('a fulfilled body that is not an ActionResult counts as OK, as before', async () => {
    await bulkStart(['a'], [{ status: 'ok', output: '', duration: 1 }])

    expect(toast.success).toHaveBeenCalledWith('Started 1 stack')
  })

  it('a rejection still counts as not OK alongside a success', async () => {
    await bulkStart(['a', 'b'], [result('success'), new Error('boom')])

    expect(toast.warning).toHaveBeenCalledWith('Started 1, 1 failed or only partly done')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('uses the action name in the all-bad message (stop)', async () => {
    mockStop.mockResolvedValueOnce(result('partial'))
    const view = renderSelection(['a'])
    await act(async () => {
      await view.result.current.runBulk('stop')
    })

    expect(mockToastInvalid).toHaveBeenCalledWith('Could not fully stop 1 stack')
  })
})
