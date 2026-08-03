import { describe, it, expect, vi } from 'vitest'
import { classifyError } from '../error-handler'

// Regression for agent-os-yj0: the response interceptor used to reject with
// the bare response body (`error.response?.data`), which has no `status`
// field. Every status-specific branch in classifyError() was therefore dead
// code — everything fell through to the terminal 'unknown' bucket.
//
// Unlike stacksApiDelete.test.ts / directoriesApiCredentials.test.ts, this
// suite does NOT stub `interceptors.response.use` away with a no-op spy —
// it captures the real callback api.ts registers and invokes it directly,
// so the actual interceptor logic runs, not a hand-built stand-in for it.
const instance = vi.hoisted(() => ({
  get: vi.fn().mockResolvedValue({ data: {} }),
  post: vi.fn().mockResolvedValue({ data: {} }),
  put: vi.fn().mockResolvedValue({ data: {} }),
  delete: vi.fn().mockResolvedValue({ data: {} }),
  interceptors: {
    request: { use: vi.fn() },
    response: { use: vi.fn() },
  },
}))

vi.mock('axios', () => ({
  default: { create: () => instance },
  AxiosError: class AxiosError extends Error {},
}))

import '@/lib/api'

function getRegisteredRejectedHandler(): (error: unknown) => Promise<never> {
  const calls = instance.interceptors.response.use.mock.calls
  expect(calls.length).toBeGreaterThan(0)
  // apiClient.interceptors.response.use(onFulfilled, onRejected)
  return calls[0][1] as (error: unknown) => Promise<never>
}

describe('api response interceptor error handling (agent-os-yj0)', () => {
  it('propagates the HTTP status through the interceptor so classifyError selects the 409 branch, not unknown', async () => {
    const onRejected = getRegisteredRejectedHandler()

    // Shaped like a real AxiosError for a 409 DUPLICATE_STACK response
    // (backend AppError wire shape: {code, message, details}).
    const fakeAxiosError = {
      response: {
        status: 409,
        data: {
          code: 'DUPLICATE_STACK',
          message: "Stack 'myapp' is already being created or modified by another operation",
        },
      },
      config: { url: '/stacks' },
      message: 'Request failed with status code 409',
      isAxiosError: true,
    }

    let rejected: unknown
    await onRejected(fakeAxiosError).catch((e) => {
      rejected = e
    })

    // The rejected value must carry `status` — this is the crux of the bug.
    expect((rejected as { status?: number }).status).toBe(409)

    const classified = classifyError(rejected)
    expect(classified.status).toBe(409)
    expect(classified.type).not.toBe('unknown')
    expect(classified.action).not.toBe('Contact Support')
    expect(classified.retryable).toBe(false)
    expect(classified.message).toBe(
      "Stack 'myapp' is already being created or modified by another operation",
    )
  })

  it('propagates the HTTP status through the interceptor so classifyError selects the 428 branch, not unknown', async () => {
    const onRejected = getRegisteredRejectedHandler()

    const fakeAxiosError = {
      response: {
        status: 428,
        data: {
          code: 'STACK_DELETE_COLLATERAL',
          message: 'Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed',
          details: { directory: '/opt/stacks/my-stack', collateral: ['data', '.git'] },
        },
      },
      config: { url: '/stacks/my-stack' },
      message: 'Request failed with status code 428',
      isAxiosError: true,
    }

    let rejected: unknown
    await onRejected(fakeAxiosError).catch((e) => {
      rejected = e
    })

    expect((rejected as { status?: number }).status).toBe(428)

    const classified = classifyError(rejected)
    expect(classified.status).toBe(428)
    expect(classified.type).not.toBe('unknown')
    expect(classified.action).toBe('Confirm')
    expect(classified.retryable).toBe(false)
    // `details` must survive the interceptor too, not just `status` — this is
    // the STACK_DELETE_COLLATERAL directory from agent-os-lg2's backend payload.
    expect(classified.context).toBe('/opt/stacks/my-stack')
  })

  it('propagates `details` through the interceptor so the 422 branch produces field-level validation messages, not the generic fallback', async () => {
    const onRejected = getRegisteredRejectedHandler()

    const fakeAxiosError = {
      response: {
        status: 422,
        data: {
          code: 'VALIDATION_ERROR',
          message: 'Validation failed',
          details: { name: 'is required', directory: 'must be an absolute path' },
        },
      },
      config: { url: '/stacks' },
      message: 'Request failed with status code 422',
      isAxiosError: true,
    }

    let rejected: unknown
    await onRejected(fakeAxiosError).catch((e) => {
      rejected = e
    })

    const classified = classifyError(rejected)
    expect(classified.status).toBe(422)
    expect(classified.type).toBe('validation')
    // Field-level messages assembled from `details` — proves `details` is not
    // silently dropped the same way `status` was (agent-os-yj0 follow-up).
    expect(classified.message).toContain('name: is required')
    expect(classified.message).toContain('directory: must be an absolute path')
  })

  it('preserves the axios code and message for a no-response network failure, instead of stomping them with a generic UNKNOWN', async () => {
    const onRejected = getRegisteredRejectedHandler()

    // A network failure never gets a `response` at all.
    const fakeAxiosError = {
      code: 'ERR_NETWORK',
      message: 'Network Error',
      config: { url: '/stacks' },
      isAxiosError: true,
    }

    let rejected: unknown
    await onRejected(fakeAxiosError).catch((e) => {
      rejected = e
    })

    const rejectedTyped = rejected as { code?: string; message?: string }
    expect(rejectedTyped.code).toBe('ERR_NETWORK')
    expect(rejectedTyped.message).toBe('Network Error')

    const classified = classifyError(rejected)
    expect(classified.type).toBe('network')
    expect(classified.retryable).toBe(true)
    expect(classified.action).toBe('Retry')
  })

  it('preserves the axios code and message for a no-response timeout failure', async () => {
    const onRejected = getRegisteredRejectedHandler()

    const fakeAxiosError = {
      code: 'ECONNABORTED',
      message: 'timeout of 120000ms exceeded',
      config: { url: '/stacks' },
      isAxiosError: true,
    }

    let rejected: unknown
    await onRejected(fakeAxiosError).catch((e) => {
      rejected = e
    })

    const rejectedTyped = rejected as { code?: string; message?: string }
    expect(rejectedTyped.code).toBe('ECONNABORTED')
    expect(rejectedTyped.message).toBe('timeout of 120000ms exceeded')

    const classified = classifyError(rejected)
    expect(classified.type).toBe('timeout')
    expect(classified.retryable).toBe(true)
    expect(classified.action).toBe('Retry')
  })

  it('does not mutate error.response.data when injecting status', async () => {
    const onRejected = getRegisteredRejectedHandler()
    const originalData = { code: 'STACK_NOT_FOUND', message: 'not found' }
    const fakeAxiosError = {
      response: { status: 404, data: originalData },
      config: { url: '/stacks/x' },
      message: 'Request failed with status code 404',
      isAxiosError: true,
    }

    await onRejected(fakeAxiosError).catch(() => {})

    expect(originalData).toEqual({ code: 'STACK_NOT_FOUND', message: 'not found' })
    expect('status' in originalData).toBe(false)
  })
})
