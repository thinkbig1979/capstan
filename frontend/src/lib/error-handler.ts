/**
 * Error-feedback convention (X-2). Use ONE channel per failure, never both:
 *
 *  - INLINE — for in-context form/validation/auth failures, next to the field or
 *    submit button the user is looking at (e.g. LoginForm/AuthPage, the compose
 *    editor lint panel). The user's attention is already there; a toast would
 *    duplicate it.
 *  - TOAST  — for background/async action outcomes the user fired and looked away
 *    from (start/stop/restart, prune, delete, save-then-navigate). Success toasts
 *    follow the same rule.
 *
 * When a mutation both has an inline surface AND navigates/runs in the background,
 * prefer inline for the validation phase and a toast only for the async result.
 */
import { messageOrNull } from './narrow'

type ErrorType = 'network' | 'auth' | 'validation' | 'server' | 'timeout' | 'unknown'

export interface AppError {
  message: string
  type: ErrorType
  status?: number
  retryable: boolean
  originalError?: unknown
  context?: string
  action?: string
}

/**
 * Which 401s mean "your session is gone" (agent-os-318).
 *
 * The backend splits the two meanings of 401 by code — see
 * backend/internal/models/errors.go, which carries the contract.
 * SESSION_EXPIRED means the session itself cannot be used; UNAUTHORIZED means
 * a credential the user just supplied was rejected while the session stayed
 * valid. A 401 with no code did not come from this backend at all (every 401
 * it mints carries one), so it is a proxy or gateway rejecting us — also
 * session loss. Fail closed there: the alternative silently stops logging
 * anyone out behind an auth proxy.
 *
 * Both the api.ts interceptor ("log out?") and the 401 branch below ("say log
 * in again, or show what the backend said?") ask this same question, and a
 * disagreement between them IS the bug: the user was redirected to /login for
 * mistyping their own password.
 */
export function isSessionLoss(code: string | null | undefined): boolean {
  return code === 'SESSION_EXPIRED' || code == null
}

/**
 * The one place that knows how to find an HTTP status on a rejection.
 *
 * The api.ts interceptor rejects with a FLAT object carrying `status` when
 * `error.response` existed, and with `{error, code, message}` and NO status
 * when it did not (api.ts:127-131). The nested `response.status` form is read
 * too, because test fixtures and any raw AxiosError that reaches us still
 * carry it. So `undefined` here means exactly one thing: the server never
 * answered.
 */
function readStatus(error: unknown): number | undefined {
  if (!error) return undefined
  const err = error as { status?: number; response?: { status?: number } }
  return err.status ?? err.response?.status
}

/**
 * Whether an AUTOMATIC retry could plausibly change the outcome (agent-os-8ett).
 *
 * Deliberately NOT `classifyError().retryable`, which answers a different
 * question: whether to enable the user-facing Retry button (DashboardPage.tsx,
 * StackPage.tsx, both `disabled={!appError.retryable}`). A person pressing
 * Retry after a 500 has waited, and may know something we do not; a query
 * retrying itself a second later knows nothing new. So a 5xx keeps enabling
 * the button but does not auto-retry -- the same call agent-os-2cp3 made for
 * `checkAuth`, which retries only when `status === undefined`.
 *
 * The rule: retry only when the server gave no definitive answer. That is no
 * HTTP status at all (network error, timeout, connection refused, offline),
 * or one of the two statuses that explicitly invite a repeat -- 408 Request
 * Timeout and 429 Too Many Requests, both of which classifyError also treats
 * as retryable.
 */
export function isAutoRetryable(error: unknown): boolean {
  const status = readStatus(error)
  if (status === undefined) return true
  return status === 408 || status === 429
}

export function classifyError(error: unknown): AppError {
  if (!error) {
    return {
      message: 'An unexpected error occurred',
      type: 'unknown',
      retryable: false,
    }
  }

  const err = error as {
    status?: number;
    details?: Record<string, unknown>;
    response?: { status?: number; data?: { error?: unknown; message?: unknown; code?: string; details?: Record<string, unknown> } };
    code?: string;
    // agent-os-06c1: `unknown`, not `string`. Nobody validated this body, so a
    // `string` here is a claim tsc would then stop checking — declaring what
    // is actually known forces every reader through messageOrNull.
    message?: unknown
  }
  // The interceptor (api.ts) rejects with a flat object carrying `status` AND
  // `details` at the top level, not nested under `.response` (agent-os-yj0).
  // Read both so existing test fixtures built as `{response:{status,data}}`
  // still work. `details` feeds the 404/409/428 `context` fields and the 422
  // field-level validation messages below — missing it silently degrades
  // those to their generic fallback text.
  const status = readStatus(error)
  // agent-os-06c1: narrowed, not asserted. `error` is `unknown` and `message`
  // is declared `string` on AppError, but it also reaches `.toLowerCase()`
  // further down — so an unnarrowed non-string throws rather than merely
  // rendering oddly. messageOrNull keeps the `||` semantics this chain had:
  // an empty candidate falls through to the next one exactly as before.
  //
  // Split in two deliberately (agent-os-mc4i). `backendMessage` is null when the
  // response carried no message at all; `message` keeps the old always-a-string
  // shape for every existing reader. Branches that substitute a fixed sentence
  // need to tell "the backend said nothing" from "the backend said something",
  // and `message` cannot express that — it is never empty, so the `message || ...`
  // idiom in the 409 and 428 arms below can never actually reach its fallback.
  const backendMessage =
    messageOrNull(err.response?.data?.error) ??
    messageOrNull(err.response?.data?.message) ??
    messageOrNull(err.message)
  const message = backendMessage ?? 'An error occurred'
  const details = err.details ?? err.response?.data?.details
  // Nested FIRST, unlike `status` and `details` above, and deliberately so:
  // axios stamps its OWN code at the top level on a 4xx — settle.js:21 rejects
  // with AxiosError.ERR_BAD_REQUEST for status 400-499 (AxiosError.js:182) —
  // so on a raw AxiosError `err.code` is 'ERR_BAD_REQUEST', not the backend's.
  // The response body is the only place the backend's code ever appears, so it
  // wins whenever there is a body. The flat fallback serves the interceptor's
  // own shape, which hoists the body's `code` to the top level.
  const backendCode = err.response?.data?.code ?? err.code

  if (err.code === 'ECONNABORTED' || err.code === 'ETIMEDOUT' || status === 408) {
    return {
      message: 'The request timed out. Please try again.',
      type: 'timeout',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (err.code === 'ERR_NETWORK' || !navigator.onLine) {
    return {
      message: 'Check your connection and try again',
      type: 'network',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (status === 401) {
    const sessionLoss = isSessionLoss(backendCode)
    return {
      // Telling a user with a valid session to "log in again" because they
      // mistyped their current password is the visible half of agent-os-318.
      message: sessionLoss ? 'Log in again to continue' : message,
      type: 'auth',
      status,
      retryable: false,
      originalError: error,
      // The session is fine on the non-expiry path — the recovery is to retype
      // the credential, not to log in.
      action: sessionLoss ? 'Log In' : 'Fix',
    }
  }

  if (status === 403) {
    return {
      // agent-os-mc4i: this arm used to hardcode the sentence and drop the
      // backend's message unconditionally. Every 403 this backend emits carries
      // more than "not permitted" — settings.go:317 and env.go:197 carry the
      // RECOVERY ("Re-enter your password to edit ..."), csrf.go:59 carries
      // "Reload the page and retry", health.go:84 names the env var to set — so
      // the hardcoded sentence told the operator they were refused when the
      // truth was that they needed to re-authenticate. All 13 backend 403
      // emissions were read before this changed; none is sensitive and none
      // interpolates client-supplied text.
      message: backendMessage ?? 'You do not have permission to perform this action',
      type: 'auth',
      status,
      retryable: false,
      originalError: error,
      action: 'Log In',
    }
  }

  if (status === 404) {
    return {
      // agent-os-mc4i: this arm used to hardcode the sentence and drop the
      // backend's message unconditionally. All 33 backend 404 emissions were
      // read before this changed. Every one names the resource that was absent
      // -- "Env file not found on disk", "No env file associated with this
      // stack", "Compose file not found on disk", "Not a git repository",
      // "Repository has no commits yet", "Stack directory does not exist on
      // disk", "Backup run not found", "Job not found", "Directory not found",
      // "Stack not found" -- which is strictly more than "the requested
      // resource". None is sensitive; the one that interpolates an error string
      // (BackupHandler.wsAttach, backend/internal/handlers/backup.go) is on a
      // WebSocket handshake, which classifyError never observes.
      //
      // `backendMessage`, not `message`: `message` ends in `?? 'An error
      // occurred'` and is therefore never empty, so it cannot distinguish "the
      // backend said nothing" from "the backend said something" and the
      // `message || ...` idiom used by the 409 and 428 arms can never reach its
      // fallback.
      message: backendMessage ?? 'The requested resource was not found',
      type: 'server',
      status,
      retryable: false,
      originalError: error,
      // agent-os-06c1: `details` is Record<string, unknown>, so `as string`
      // here asserts a shape nobody validated, exactly as `as { ... }` does
      // elsewhere in this class. `context` is rendered.
      context: messageOrNull(details?.resource) ?? undefined,
    }
  }

  if (status === 409) {
    return {
      message: message || 'This conflicts with an operation already in progress. Wait for it to finish and try again.',
      type: 'server',
      status,
      retryable: false,
      originalError: error,
      // agent-os-06c1: `details` is Record<string, unknown>, so `as string`
      // here asserts a shape nobody validated, exactly as `as { ... }` does
      // elsewhere in this class. `context` is rendered.
      context: messageOrNull(details?.resource) ?? undefined,
      action: 'Refresh',
    }
  }

  if (status === 422 || status === 400) {
    const fieldErrors = details as Record<string, string> | undefined
    const fieldMessage = fieldErrors 
      ? Object.entries(fieldErrors).map(([field, err]) => `${field}: ${err}`).join(', ')
      : message

    return {
      message: fieldMessage || 'Please check your input and try again',
      type: 'validation',
      status,
      retryable: false,
      originalError: error,
      action: 'Fix',
    }
  }

  if (status === 428) {
    return {
      message: message || 'Deleting this would also remove other files. Confirm to proceed.',
      type: 'server',
      status,
      retryable: false,
      originalError: error,
      // agent-os-06c1: see the 404 arm above.
      context: messageOrNull(details?.directory) ?? undefined,
      action: 'Confirm',
    }
  }

  if (status === 429) {
    return {
      message: 'Too many requests. Please wait a moment and try again',
      type: 'server',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (status && status >= 500) {
    return {
      message: `${status}: Something went wrong on the server`,
      type: 'server',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (message.toLowerCase().includes('network') || message.toLowerCase().includes('fetch failed')) {
    return {
      message: 'Check your connection and try again',
      type: 'network',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (message.toLowerCase().includes('validation') || message.toLowerCase().includes('invalid')) {
    return {
      message: message || 'Please check your input and try again',
      type: 'validation',
      status,
      retryable: false,
      originalError: error,
      action: 'Fix',
    }
  }

  return {
    message: message || 'An unexpected error occurred',
    type: 'unknown',
    status,
    retryable: true,
    originalError: error,
    action: 'Contact Support',
  }
}
