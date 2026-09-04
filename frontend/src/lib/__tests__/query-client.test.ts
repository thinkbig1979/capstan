import { describe, it, expect } from 'vitest'
import { queryClient } from '../query-client'
import { classifyError, isAutoRetryable } from '../error-handler'

/**
 * Evaluates the client's CONFIGURED default `retry` the way react-query itself
 * would, whatever shape it is in (agent-os-8ett). Deliberately shape-agnostic:
 * it models the `number` form too, so this file compiles and runs against the
 * pre-fix `retry: 1` and fails on its ASSERTIONS rather than on the build.
 */
function willRetry(failureCount: number, error: unknown): boolean {
  const retry = queryClient.getDefaultOptions().queries?.retry
  if (typeof retry === 'function') return retry(failureCount, error as Error)
  if (typeof retry === 'number') return failureCount < retry
  return retry === true
}

// The api.ts interceptor's own two reject shapes (api.ts:127-131): a flat
// `status` when there was a response, and no `status` at all when there wasn't.
const withStatus = (status: number) => ({ error: 'nope', status })
const noResponse = { error: 'Unknown error', code: 'ERR_NETWORK', message: 'Network Error' }

describe('queryClient default retry', () => {
  describe('does NOT retry a definitive answer from the server', () => {
    it.each([401, 403, 404, 409, 422, 428, 500, 502, 503])('%i', (status) => {
      expect(willRetry(0, withStatus(status))).toBe(false)
    })
  })

  describe('DOES retry when the server never answered', () => {
    it('a network error carrying no status', () => {
      expect(willRetry(0, noResponse)).toBe(true)
    })

    it('an aborted/timed-out request carrying no status', () => {
      expect(willRetry(0, { code: 'ECONNABORTED', message: 'timeout of 30000ms exceeded' })).toBe(true)
    })

    it('but only once — the failure cap still applies', () => {
      expect(willRetry(1, noResponse)).toBe(false)
    })
  })

  describe('DOES retry the two statuses that invite a repeat', () => {
    it('408 Request Timeout', () => {
      expect(willRetry(0, withStatus(408))).toBe(true)
    })

    it('429 Too Many Requests', () => {
      expect(willRetry(0, withStatus(429))).toBe(true)
    })
  })

  it('reads a nested response.status too, not just the flat interceptor shape', () => {
    expect(willRetry(0, { response: { status: 404 } })).toBe(false)
  })
})

/**
 * The divergence this bead exists to remove is now two LABELLED notions rather
 * than two unlabelled ones, so both labels get pinned here on the same status.
 * A 5xx must auto-retry NO and enable the button YES; drop either arm and the
 * next reader cannot tell the split was deliberate.
 */
describe('the two notions of retryable are split, not collapsed', () => {
  it('a 5xx does not auto-retry but still enables the user-facing Retry button', () => {
    expect(isAutoRetryable({ status: 500 })).toBe(false)
    expect(classifyError({ status: 500 }).retryable).toBe(true)
  })

  it('a 404 does neither', () => {
    expect(isAutoRetryable({ status: 404 })).toBe(false)
    expect(classifyError({ status: 404 }).retryable).toBe(false)
  })

  it('a no-response failure does both', () => {
    const err = { code: 'ERR_NETWORK', message: 'Network Error' }
    expect(isAutoRetryable(err)).toBe(true)
    expect(classifyError(err).retryable).toBe(true)
  })
})
