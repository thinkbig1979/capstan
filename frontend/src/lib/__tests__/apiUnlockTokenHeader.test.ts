import { describe, it, expect, vi, beforeEach } from 'vitest'

/**
 * The env-unlock token is what makes the backend answer the secret surfaces in
 * full (agent-os-7o5s). If the request interceptor does not attach it, every
 * reveal silently shows a blank value and the whole flow looks broken while
 * being perfectly "successful" — so the header is worth asserting directly.
 *
 * Follows apiInterceptorError.test.ts: the real callback api.ts registers is
 * captured and invoked, rather than a hand-built stand-in for it.
 */
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
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

function runRequestInterceptor(): Record<string, unknown> {
  const calls = instance.interceptors.request.use.mock.calls
  expect(calls.length).toBeGreaterThan(0)
  const onFulfilled = calls[0][0] as (config: { headers: Record<string, unknown> }) => {
    headers: Record<string, unknown>
  }
  return onFulfilled({ headers: {} }).headers
}

describe('api request interceptor: X-Unlock-Token (agent-os-7o5s)', () => {
  beforeEach(() => {
    useEnvUnlockStore.getState().lock()
  })

  it('sends no unlock header while locked', () => {
    expect(runRequestInterceptor()['X-Unlock-Token']).toBeUndefined()
  })

  it('sends the minted token once the unlock window is open', () => {
    useEnvUnlockStore.getState().unlock('minted-token-abc')

    expect(runRequestInterceptor()['X-Unlock-Token']).toBe('minted-token-abc')
  })

  it('stops sending it after the window is locked again', () => {
    useEnvUnlockStore.getState().unlock('minted-token-abc')
    useEnvUnlockStore.getState().lock()

    expect(runRequestInterceptor()['X-Unlock-Token']).toBeUndefined()
  })

  it('sends no header when the server minted no token', () => {
    // An older backend, or a minting failure, answers ok:true with no token.
    // Sending "null" as a header value would be worse than sending nothing.
    useEnvUnlockStore.getState().unlock(null)

    expect(runRequestInterceptor()['X-Unlock-Token']).toBeUndefined()
  })
})
