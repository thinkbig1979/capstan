import { describe, it, expect, vi, beforeEach, afterAll } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

/**
 * The class this file pins (agent-os-tdts): a per-query option that RESTATES
 * the client default silently opts that query out of every future change to
 * the default, because react-query REPLACES the default rather than composing
 * with it. 23 queries carried `retry: 1` -- the value the client default used
 * to hold -- and so did not receive agent-os-8ett's retry predicate.
 *
 * These tests drive the REAL app `queryClient` (not a test-local one), because
 * the thing under test is whether the app's configured policy actually reaches
 * a query hook.
 */

const mockImages = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: { images: (...args: unknown[]) => mockImages(...args) },
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: {
    loading: vi.fn(), success: vi.fn(), error: vi.fn(),
    dismiss: vi.fn(), warning: vi.fn(), info: vi.fn(),
  },
}))

import { queryClient } from '../query-client'
import { useImages } from '@/hooks/useResources'

// The interceptor's two reject shapes (api.ts:127-131): a flat `status` when a
// response came back, and no `status` at all when none did.
const withStatus = (status: number) => ({ error: 'nope', status })
const noResponse = { error: 'Unknown error', code: 'ERR_NETWORK', message: 'Network Error' }

const appDefaults = queryClient.getDefaultOptions()

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
)

beforeEach(() => {
  vi.clearAllMocks()
  queryClient.clear()
  // `retry` is carried through UNCHANGED -- it is the thing under test. Only
  // the delay and the caches are neutralised so the test does not sit through
  // react-query's 1s backoff.
  queryClient.setDefaultOptions({
    ...appDefaults,
    queries: { ...appDefaults.queries, retryDelay: 0, gcTime: 0, staleTime: 0 },
  })
})

afterAll(() => {
  queryClient.setDefaultOptions(appDefaults)
})

describe('a query that used to carry `retry: 1` now follows the client predicate', () => {
  it.each([401, 403, 404, 500])(
    'does not auto-retry a definitive %i -- one request, then the error',
    async (status) => {
      mockImages.mockRejectedValue(withStatus(status))

      const { result } = renderHook(() => useImages(), { wrapper })
      await waitFor(() => expect(result.current.isError).toBe(true))

      expect(mockImages).toHaveBeenCalledTimes(1)
    },
  )

  // The load-bearing other half: the change must DISCRIMINATE transient from
  // definitive, not simply stop retrying. Exactly 2 also pins the one-retry cap
  // -- a predicate that forgot `failureCount < 1` would loop here.
  it('still retries once when the server never answered, and only once', async () => {
    mockImages.mockRejectedValue(noResponse)

    const { result } = renderHook(() => useImages(), { wrapper })
    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(mockImages).toHaveBeenCalledTimes(2)
  })
})

/**
 * The mechanism, stated as the falsifying control rather than taken from the
 * docs: both arms on one instrument, so a react-query version that started
 * MERGING the two would fail here instead of silently making the bead moot.
 */
describe('the mechanism: a per-query retry replaces the client default', () => {
  it('a query with no retry of its own inherits the client predicate', () => {
    const clientDefault = queryClient.getDefaultOptions().queries?.retry
    expect(typeof clientDefault).toBe('function')
    expect(queryClient.defaultQueryOptions({ queryKey: ['probe-inherits'] }).retry)
      .toBe(clientDefault)
  })

  it('a query that restates `retry: 1` gets 1, NOT the predicate', () => {
    expect(queryClient.defaultQueryOptions({ queryKey: ['probe-replaces'], retry: 1 }).retry)
      .toBe(1)
  })
})
