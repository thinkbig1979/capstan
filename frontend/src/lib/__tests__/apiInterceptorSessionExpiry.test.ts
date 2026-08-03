import { describe, it, expect, vi, beforeEach } from 'vitest'

// Regression for agent-os-318: mistyping your CURRENT password on Settings ->
// Change Password, or in the env-unlock dialog, returned 401 and the response
// interceptor treated it as session expiry — hard-navigating to /login while
// the session was still perfectly valid (`GET /auth/me` returned 200 right
// after the redirect).
//
// The contract this suite pins: the session guard owns the signal. A 401
// carrying SESSION_EXPIRED (or no code at all, which cannot have come from
// this backend) means the session is gone and must log out; a 401 carrying
// UNAUTHORIZED means the credential the user just typed was rejected and the
// session is untouched. See backend/internal/models/errors.go.
//
// This is a sibling of apiInterceptorError.test.ts rather than an extension of
// it: these cases require setAuthCallbacks(), which writes module-level state
// shared by every test in a file, and that file's premise is that it never
// sets them. Vitest gives each file its own module registry, so a sibling gets
// that isolation for free. The harness below is that file's — capture the real
// callback api.ts registers and invoke it, rather than hand-building a
// stand-in for the interceptor.
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

import { setAuthCallbacks } from '@/lib/api'

const logout = vi.fn()

function getRegisteredRejectedHandler(): (error: unknown) => Promise<never> {
  const calls = instance.interceptors.response.use.mock.calls
  expect(calls.length).toBeGreaterThan(0)
  // apiClient.interceptors.response.use(onFulfilled, onRejected)
  return calls[0][1] as (error: unknown) => Promise<never>
}

/** Shaped like the AxiosError axios really hands the interceptor. */
function axiosErrorFor(url: string, status: number, data: unknown) {
  return {
    response: { status, data },
    config: { url },
    message: `Request failed with status code ${status}`,
    isAxiosError: true,
  }
}

async function runInterceptor(error: unknown) {
  await getRegisteredRejectedHandler()(error).catch(() => {})
}

describe('api response interceptor session-expiry handling (agent-os-318)', () => {
  beforeEach(() => {
    logout.mockClear()
    // api.ts short-circuits on `&& logout`; App.tsx registers the real one at
    // boot (App.tsx:57-64), so an unregistered callback would make every
    // assertion below vacuously pass.
    setAuthCallbacks(() => null, logout)
  })

  it('logs out on a SESSION_EXPIRED 401 from an ordinary endpoint', async () => {
    await runInterceptor(
      axiosErrorFor('/stacks', 401, { code: 'SESSION_EXPIRED', message: 'Session expired' }),
    )
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('does NOT log out when the change-password endpoint rejects the current password', async () => {
    await runInterceptor(
      axiosErrorFor('/auth/password', 401, {
        code: 'UNAUTHORIZED',
        message: 'Current password is incorrect',
      }),
    )
    expect(logout).not.toHaveBeenCalled()
  })

  it('does NOT log out when the env-unlock probe rejects the password', async () => {
    await runInterceptor(
      axiosErrorFor('/auth/verify-password', 401, {
        code: 'UNAUTHORIZED',
        message: 'Invalid password',
      }),
    )
    expect(logout).not.toHaveBeenCalled()
  })

  it('does NOT log out on the /auth/me boot probe, even though its 401 is a real SESSION_EXPIRED', async () => {
    // /auth/me sits behind AuthMiddleware, so a logged-out boot genuinely
    // returns SESSION_EXPIRED. Redirecting on it would reload /login forever:
    // App.tsx:66-77 probes on every boot and the logout callback navigates
    // with window.location.href. The auth store handles this 401 itself.
    await runInterceptor(
      axiosErrorFor('/auth/me', 401, { code: 'SESSION_EXPIRED', message: 'Session expired' }),
    )
    expect(logout).not.toHaveBeenCalled()
  })

  it('logs out on a 401 with no code at all — fail closed, it did not come from this backend', async () => {
    // Every 401 the backend mints carries a code, so an absent one means a
    // proxy or gateway rejected us. Treating that as "not session loss" would
    // silently stop logging anyone out behind an auth proxy.
    await runInterceptor(axiosErrorFor('/stacks', 401, 'Unauthorized'))
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('does not log out on a non-401', async () => {
    await runInterceptor(
      axiosErrorFor('/stacks', 403, { code: 'FORBIDDEN', message: 'Nope' }),
    )
    expect(logout).not.toHaveBeenCalled()
  })
})
