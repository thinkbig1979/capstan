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
    response?: { status?: number; data?: { error?: string; message?: string; code?: string; details?: Record<string, unknown> } };
    code?: string;
    message?: string
  }
  // The interceptor (api.ts) rejects with a flat object carrying `status` AND
  // `details` at the top level, not nested under `.response` (agent-os-yj0).
  // Read both so existing test fixtures built as `{response:{status,data}}`
  // still work. `details` feeds the 404/409/428 `context` fields and the 422
  // field-level validation messages below — missing it silently degrades
  // those to their generic fallback text.
  const status = readStatus(error)
  const message = err.response?.data?.error || err.response?.data?.message || err.message || 'An error occurred'
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
      message: 'You do not have permission to perform this action',
      type: 'auth',
      status,
      retryable: false,
      originalError: error,
      action: 'Log In',
    }
  }

  if (status === 404) {
    return {
      message: 'The requested resource was not found',
      type: 'server',
      status,
      retryable: false,
      originalError: error,
      context: details?.resource as string,
    }
  }

  if (status === 409) {
    return {
      message: message || 'This conflicts with an operation already in progress. Wait for it to finish and try again.',
      type: 'server',
      status,
      retryable: false,
      originalError: error,
      context: details?.resource as string,
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
      context: details?.directory as string,
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
