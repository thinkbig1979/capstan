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

  // agent-os-mc4i: the 403 branch used to hardcode its sentence and discard the
  // backend's message unconditionally. Every 403 this backend emits carries
  // something MORE useful than "you do not have permission" — several carry the
  // recovery itself — so replacing them told the operator the opposite of the
  // truth: refused, when the truth was "re-authenticate".
  describe('403 preserves the backend cause (agent-os-mc4i)', () => {
    it('keeps the recovery sentence from the interceptor-shaped body', () => {
      const result = classifyError({
        status: 403,
        code: 'FORBIDDEN',
        message: 'Re-enter your password to edit global environment variables',
      })
      expect(result.message).toBe('Re-enter your password to edit global environment variables')
      expect(result.type).toBe('auth')
      expect(result.status).toBe(403)
    })

    it('keeps the cause from a nested axios-shaped body', () => {
      const result = classifyError({
        response: { status: 403, data: { message: 'Cannot change password when auth is disabled' } },
      })
      expect(result.message).toBe('Cannot change password when auth is disabled')
    })

    it('keeps the CSRF recovery', () => {
      const result = classifyError({
        status: 403,
        code: 'CSRF_COOKIE_MISSING',
        message: 'CSRF cookie required. Reload the page and retry.',
      })
      expect(result.message).toBe('CSRF cookie required. Reload the page and retry.')
    })

    // TWO-SIDED: a 403 with no body carries nothing to show, and must still get
    // the generic sentence rather than 'An error occurred'. This one is green
    // before the fix and must stay green after it.
    it('falls back to the generic sentence when the body carries no message', () => {
      const result = classifyError({ status: 403 })
      expect(result.message).toBe('You do not have permission to perform this action')
    })
  })

  // agent-os-mc4i: the 404 branch used to hardcode its sentence and discard the
  // backend's message unconditionally. All 33 backend 404 emissions were read
  // first: every one names the resource that was absent ("Env file not found on
  // disk", "Not a git repository", "Repository has no commits yet", "Backup run
  // not found"), which is strictly more than "the requested resource". None is
  // sensitive and none interpolates client-supplied text on a path classifyError
  // can observe.
  describe('404 preserves the backend cause (agent-os-mc4i)', () => {
    it('keeps the cause from the flat interceptor shape', () => {
      const result = classifyError({
        status: 404,
        code: 'NOT_FOUND',
        message: 'Env file not found on disk',
      })
      expect(result.message).toBe('Env file not found on disk')
      expect(result.type).toBe('server')
      expect(result.status).toBe(404)
      expect(result.retryable).toBe(false)
    })

    it('keeps the cause from a nested axios-shaped body', () => {
      const result = classifyError({
        response: { status: 404, data: { message: 'Not a git repository' } },
      })
      expect(result.message).toBe('Not a git repository')
    })

    // The two git 404s a Class B site renders side by side. They are the reason
    // the branch mattered: both were shown as one indistinguishable sentence.
    it('tells the two git 404s apart', () => {
      const notRepo = classifyError({ status: 404, code: 'GIT_NOT_REPO', message: 'Not a git repository' })
      const noCommits = classifyError({ status: 404, code: 'GIT_NO_COMMITS', message: 'Repository has no commits yet' })
      expect(notRepo.message).not.toBe(noCommits.message)
      expect(notRepo.message).toBe('Not a git repository')
      expect(noCommits.message).toBe('Repository has no commits yet')
    })

    // The 404 arm ALSO sets `context` from details.resource. The cause and the
    // context are separate fields and both must survive the same response.
    it('keeps context and cause together, not one at the expense of the other', () => {
      const result = classifyError({
        status: 404,
        message: 'Stack not found',
        details: { resource: 'stacks~myapp:default' },
      })
      expect(result.message).toBe('Stack not found')
      expect(result.context).toBe('stacks~myapp:default')
    })

    // TWO-SIDED: a 404 with no body carries nothing to show, and must still get
    // the generic sentence rather than 'An error occurred'. Green before the fix
    // and must stay green after it. `message` cannot express this case -- it is
    // never empty (`?? 'An error occurred'`) -- which is why the arm reads
    // `backendMessage`.
    it('falls back to the generic sentence when the body carries no message', () => {
      const result = classifyError({ status: 404 })
      expect(result.message).toBe('The requested resource was not found')
    })

    // A proxy or gateway 404, whose HTML body the interceptor spreads into
    // something with no `message` at all. Same requirement as above by a
    // different route.
    it('falls back when the body is not this backend error shape', () => {
      const result = classifyError({ response: { status: 404, data: '<html>404</html>' } })
      expect(result.message).toBe('The requested resource was not found')
    })

    // 404 becomes a THIRD escape path for a non-string message once the arm
    // stops minting its own sentence -- the same shape as the 409 arm's test.
    it('does not let a non-string message escape on the 404 route', () => {
      const result = classifyError({ response: { status: 404, data: { message: { a: 1 } } } })
      expect(typeof result.message).toBe('string')
      expect(result.message).toBe('The requested resource was not found')
    })
  })

  it('classifies 404 as server', () => {
    const result = classifyError({
      response: { status: 404, data: { error: 'Not Found' } },
      message: 'Not Found',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(false)
  })

  // agent-os-mc4i: the 5xx arm discarded the backend's message entirely and
  // answered a bare status code. All 138 5xx emission regions were read first.
  // AppError.Cause is `json:"-"` and never serialized, so Message is the
  // backend's own sanitised, client-safe string and is ALREADY on the wire --
  // the only thing that changed is whether the frontend renders what it already
  // received. Of 101 distinct message literals only two are the generic
  // "Internal server error"; exactly two emissions interpolate anything, and
  // both interpolate server-controlled values, not client input.
  //
  // The status stays in the rendered string. This arm is the one place where
  // the status itself is diagnostic -- 503 means "try later / Docker is down"
  // and 500 means "this is a bug" -- so the cause is ADDED to what was shown
  // before rather than swapped for it.
  describe('5xx preserves the backend cause (agent-os-mc4i)', () => {
    it('keeps the cause and the status together', () => {
      const result = classifyError({
        status: 500,
        code: 'INTERNAL_ERROR',
        message: 'Failed to read the backup retention and schedule settings',
      })
      expect(result.message).toBe('500: Failed to read the backup retention and schedule settings')
      expect(result.type).toBe('server')
      expect(result.retryable).toBe(true)
      expect(result.action).toBe('Retry')
    })

    // The whole reason the arm mattered: DockerUnavailableMessage
    // (handlers/respond.go:186) is a 503 carrying the RECOVERY, and it was
    // rendered as the three characters "503".
    it('keeps the Docker outage recovery text on a 503', () => {
      const result = classifyError({
        status: 503,
        code: 'DOCKER_UNAVAILABLE',
        message:
          'Docker daemon unreachable: the server started without a usable Docker connection. Check that the Docker socket is mounted and the daemon is running, then restart Capstan.',
      })
      expect(result.message).toContain('Check that the Docker socket is mounted')
      expect(result.message).toContain('503')
    })

    // 502 exists here too (services/git.go:501) and must take the same route,
    // not fall past the arm.
    it('covers 502 as well as 500 and 503', () => {
      const result = classifyError({
        response: { status: 502, data: { message: 'Could not read from the git remote' } },
      })
      expect(result.message).toBe('502: Could not read from the git remote')
    })

    // TWO-SIDED: a 5xx carrying no message renders exactly what it renders
    // today. Green before the fix and must stay green after it.
    it('falls back to the generic sentence when the body carries no message', () => {
      const result = classifyError({ status: 500 })
      expect(result.message).toBe('500: Something went wrong on the server')
    })

    // An ActionResult rejection has `reason`, not `message`, so backendMessage
    // is null and the fallback fires -- the shape useActionMutation already
    // relies on. Pinned here so the 5xx arm cannot quietly start reading it.
    it('does not mistake an ActionResult reason for a backend message', () => {
      const result = classifyError({ outcome: 'failed', reason: 'restic exited 10', status: 500 })
      expect(result.message).toBe('500: Something went wrong on the server')
    })

    it('does not let a non-string message escape on the 5xx route', () => {
      const result = classifyError({ response: { status: 500, data: { message: { a: 1 } } } })
      expect(typeof result.message).toBe('string')
      expect(result.message).toBe('500: Something went wrong on the server')
    })
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

  // agent-os-nud8: the compose 422s (compose.go Put/PutComposeAndEnv,
  // stack_crud.go Create) send details.lintResults as an array of objects, and
  // PUT /compose-env's rejection reaches this arm through presentError.
  it('renders non-string 422 detail values as JSON, not [object Object]', () => {
    const lint = { level: 'error', message: 'bad port', line: 3 }
    const result = classifyError({
      status: 422,
      code: 'COMPOSE_VALIDATION_ERROR',
      message: 'Compose file validation failed',
      details: { saved: false, retries: 2, lintResults: [lint], meta: { a: 1 }, gone: null, name: 'is required' },
    })
    expect(result.message).not.toContain('[object Object]')
    expect(result.message).toBe(
      `saved: false, retries: 2, lintResults: ${JSON.stringify([lint])}, meta: {"a":1}, gone: null, name: is required`,
    )
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

  // agent-os-06c1: classifyError reaches AppError.message, declared `string`,
  // through an unchecked `error as { message?: string }` assertion. tsc cannot
  // see a non-string escaping it, so this is the arm that can.
  it('does not let a non-string message escape into AppError.message', () => {
    // The no-status route is the one that RETURNS `message`, and it also
    // reaches `message.toLowerCase()` at error-handler.ts:243 — so an
    // unnarrowed non-string does not merely render oddly here, it throws.
    expect(() => classifyError({ message: { a: 1 } })).not.toThrow()
    expect(typeof classifyError({ message: { a: 1 } }).message).toBe('string')
    expect(classifyError({ message: { a: 1 } }).message).toBe('An error occurred')
    expect(typeof classifyError({ message: 42 }).message).toBe('string')
  })

  it('does not let a non-string message escape on the 409 route either', () => {
    // 409 renders `message || <fallback>`, so it is a SECOND escape path and
    // not the same line re-tested. 500 is deliberately NOT used: that branch
    // mints its own sentence and discards `message`, so it would pass without
    // exercising anything — the arm would agree for the wrong reason.
    const result = classifyError({ response: { status: 409, data: { error: { a: 1 } } } })
    expect(typeof result.message).toBe('string')
    expect(result.message).not.toContain('[object Object]')
  })

  it('still prefers a real string message, so the narrowing changed nothing reachable', () => {
    expect(classifyError({ message: 'Disk full' }).message).toBe('Disk full')
    expect(
      classifyError({ response: { status: 409, data: { error: 'Already deploying' } } }).message,
    ).toBe('Already deploying')
  })

  // agent-os-06c1, second route: `details` is Record<string, unknown> and the
  // 404/409/428 arms reached AppError.context through a bare `as string`.
  // The `as { ... }` sweep key cannot see this shape at all.
  it('does not let a non-string details field escape into AppError.context', () => {
    const notFound = classifyError({
      response: { status: 404, data: { error: 'Not found', details: { resource: { id: 7 } } } },
    })
    expect(notFound.context === undefined || typeof notFound.context === 'string').toBe(true)

    const collateral = classifyError({
      status: 428,
      message: 'Collateral',
      details: { directory: { path: '/srv/app' } },
    })
    expect(collateral.context === undefined || typeof collateral.context === 'string').toBe(true)
  })

  it('still carries a real string context, so nothing reachable moved', () => {
    const result = classifyError({
      response: {
        status: 404,
        data: { error: 'Not found', details: { resource: 'stacks~myapp:default' } },
      },
    })
    expect(result.context).toBe('stacks~myapp:default')
  })
})
