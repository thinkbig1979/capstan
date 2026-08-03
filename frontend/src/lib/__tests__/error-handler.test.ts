import { describe, it, expect } from 'vitest'
import { classifyError } from '../error-handler'

describe('classifyError', () => {
  it('returns unknown for null', () => {
    const result = classifyError(null)
    expect(result.type).toBe('unknown')
    expect(result.retryable).toBe(false)
  })

  it('returns unknown for undefined', () => {
    const result = classifyError(undefined)
    expect(result.type).toBe('unknown')
  })

  it('classifies ECONNABORTED as timeout', () => {
    const result = classifyError({ code: 'ECONNABORTED', message: 'timeout' })
    expect(result.type).toBe('timeout')
    expect(result.retryable).toBe(true)
  })

  it('classifies ETIMEDOUT as timeout', () => {
    const result = classifyError({ code: 'ETIMEDOUT', message: 'timed out' })
    expect(result.type).toBe('timeout')
  })

  it('classifies ERR_NETWORK as network', () => {
    const result = classifyError({ code: 'ERR_NETWORK', message: 'Network Error' })
    expect(result.type).toBe('network')
    expect(result.retryable).toBe(true)
  })

  it('classifies 401 as auth', () => {
    const result = classifyError({
      response: { status: 401, data: { error: 'Unauthorized' } },
      message: 'Unauthorized',
    })
    expect(result.type).toBe('auth')
    expect(result.retryable).toBe(false)
    expect(result.status).toBe(401)
  })

  // agent-os-318: a 401 is not automatically "log in again". The backend sends
  // UNAUTHORIZED when the credential the user just typed was rejected while the
  // session is still valid, and SESSION_EXPIRED when the session itself is gone.
  // Discarding the backend's message left both fix sites (SettingsPage.tsx:183,
  // EnvUnlockDialog.tsx:42) telling a logged-in user to log in again.
  it('surfaces the backend message for a rejected-credential 401, not the canned log-in-again string', () => {
    // Verified on the wire: PUT /auth/password with a wrong current password.
    const result = classifyError({
      status: 401,
      code: 'UNAUTHORIZED',
      message: 'Current password is incorrect',
    })
    expect(result.message).toBe('Current password is incorrect')
    expect(result.type).toBe('auth')
    expect(result.action).not.toBe('Log In')
  })

  it('surfaces the backend message for a rejected env-unlock password', () => {
    // Verified on the wire: POST /auth/verify-password with a wrong password.
    const result = classifyError({
      status: 401,
      code: 'UNAUTHORIZED',
      message: 'Invalid password',
    })
    expect(result.message).toBe('Invalid password')
  })

  // classifyError reads every other field from BOTH the flat interceptor shape
  // and the nested {response:{status,data}} shape (see its `status`/`message`/
  // `details` reads). The backend code must be read the same way, or the same
  // 401 classifies two different ways depending on which shape it arrives in.
  it('surfaces the backend message for a rejected-credential 401 in the nested response shape', () => {
    const result = classifyError({
      response: {
        status: 401,
        data: { code: 'UNAUTHORIZED', message: 'Current password is incorrect' },
      },
      message: 'Request failed with status code 401',
    })
    expect(result.message).toBe('Current password is incorrect')
    expect(result.action).not.toBe('Log In')
  })

  it('reads the backend code ahead of axios own top-level code on a 401', () => {
    // A raw AxiosError for a 4xx carries code 'ERR_BAD_REQUEST' at the top
    // level (axios settle.js:21 -> AxiosError.js:182), which sits in the same
    // field the backend's code would occupy in the flat shape. Reading the
    // nested body first keeps it from masking a genuine session expiry.
    const result = classifyError({
      code: 'ERR_BAD_REQUEST',
      response: { status: 401, data: { code: 'SESSION_EXPIRED', message: 'Session expired' } },
      message: 'Request failed with status code 401',
    })
    expect(result.message).toBe('Log in again to continue')
    expect(result.action).toBe('Log In')
  })

  it('keeps the canned log-in-again message for a genuine SESSION_EXPIRED 401', () => {
    const result = classifyError({
      status: 401,
      code: 'SESSION_EXPIRED',
      message: 'Session expired',
    })
    expect(result.message).toBe('Log in again to continue')
    expect(result.action).toBe('Log In')
  })

  it('classifies 403 as auth', () => {
    const result = classifyError({
      response: { status: 403, data: { error: 'Forbidden' } },
      message: 'Forbidden',
    })
    expect(result.type).toBe('auth')
    expect(result.status).toBe(403)
  })

  it('classifies 404 as server', () => {
    const result = classifyError({
      response: { status: 404, data: { error: 'Not Found' } },
      message: 'Not Found',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(false)
  })

  it('classifies 429 as server with retryable', () => {
    const result = classifyError({
      response: { status: 429, data: {} },
      message: 'Too Many Requests',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(true)
  })

  it('classifies 500 as server with retryable', () => {
    const result = classifyError({
      response: { status: 500, data: { error: 'Internal Server Error' } },
      message: 'Internal Server Error',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(true)
    expect(result.status).toBe(500)
  })

  it('classifies 422 as validation', () => {
    const result = classifyError({
      response: {
        status: 422,
        data: { error: 'Validation failed', details: { name: 'is required' } },
      },
      message: 'Validation failed',
    })
    expect(result.type).toBe('validation')
    expect(result.retryable).toBe(false)
    expect(result.action).toBe('Fix')
  })

  it('classifies 400 as validation', () => {
    const result = classifyError({
      response: { status: 400, data: { error: 'Bad Request' } },
      message: 'Bad Request',
    })
    expect(result.type).toBe('validation')
    expect(result.retryable).toBe(false)
  })

  it('classifies message containing "network" as network', () => {
    const result = classifyError({ message: 'A network error occurred' })
    expect(result.type).toBe('network')
    expect(result.retryable).toBe(true)
  })

  it('classifies message containing "fetch failed" as network', () => {
    const result = classifyError({ message: 'TypeError: fetch failed' })
    expect(result.type).toBe('network')
  })

  it('returns unknown for unrecognized errors', () => {
    const result = classifyError({ message: 'something weird happened' })
    expect(result.type).toBe('unknown')
    expect(result.retryable).toBe(true)
  })

  it('classifies 409 as a distinct non-retryable conflict, not Contact Support', () => {
    // Backend AppError shape (models/errors.go) is {code, message}, not {error} —
    // no "error" key, so classifyError's data.error||data.message falls through
    // to the human-readable data.message here, same as a real 409 response.
    const result = classifyError({
      response: {
        status: 409,
        data: { code: 'DUPLICATE_STACK', message: "Stack 'myapp' is already being created or modified by another operation" },
      },
      message: "Stack 'myapp' is already being created or modified by another operation",
    })
    expect(result.type).not.toBe('unknown')
    expect(result.action).not.toBe('Contact Support')
    expect(result.retryable).toBe(false)
    expect(result.status).toBe(409)
    expect(result.message).toBe("Stack 'myapp' is already being created or modified by another operation")
  })

  it('classifies 428 as a distinct precondition, not retryable, not Contact Support', () => {
    // Backend AppError shape (models/errors.go) for STACK_DELETE_COLLATERAL:
    // {code, message, details:{directory, collateral}} — Status is `json:"-"`.
    const result = classifyError({
      response: {
        status: 428,
        data: {
          code: 'STACK_DELETE_COLLATERAL',
          message: 'Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed',
          details: { directory: '/opt/stacks/my-stack', collateral: ['data', '.git'] },
        },
      },
      message: 'Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed',
    })
    expect(result.type).not.toBe('unknown')
    expect(result.action).not.toBe('Contact Support')
    expect(result.retryable).toBe(false)
    expect(result.status).toBe(428)
    expect(result.message).toBe(
      'Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed',
    )
  })

  it('extracts context from 404 details', () => {
    const result = classifyError({
      response: {
        status: 404,
        data: { error: 'Not found', details: { resource: 'stacks~myapp:default' } },
      },
      message: 'Not found',
    })
    expect(result.context).toBe('stacks~myapp:default')
  })
})
