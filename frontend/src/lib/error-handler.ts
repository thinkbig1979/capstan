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
import { toast } from 'sonner'
import { isActionResult } from './action-result'
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

/**
 * The sentence classifyError substitutes when the rejection carried no message
 * of its own. ONE constant, referenced by both the substitution below and
 * causeOf's suppression rule, so the two cannot drift apart into two literals.
 */
const NO_BACKEND_MESSAGE = 'An error occurred'

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
  //
  // Extracted to backendCauseOf (agent-os-5g8a) so the presenters below run the
  // SAME chain rather than a second copy of it.
  const backendMessage = backendCauseOf(error)
  const message = backendMessage ?? NO_BACKEND_MESSAGE
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
    // agent-os-nud8: detail values are not all strings. The compose 422s send
    // details.lintResults as an array of objects, which a template literal
    // renders as "[object Object]"; JSON keeps it readable (as agent-os-w44n).
    const fieldMessage = details
      ? Object.entries(details)
          .map(([field, value]) => `${field}: ${value !== null && typeof value === 'object' ? JSON.stringify(value) : String(value)}`)
          .join(', ')
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
      // agent-os-mc4i: this arm discarded the backend's message entirely and
      // answered a bare status code. All 138 5xx emission regions in backend/
      // were read before this changed.
      //
      // Nothing new goes on the wire: AppError.Cause is `json:"-"` and is never
      // serialized (models/errors.go), so Message is the backend's own
      // sanitised, client-safe string and the response body already carried it.
      // The only thing that changed is whether the frontend RENDERS what it
      // already received. Of 101 distinct message literals just two are the
      // generic "Internal server error"; exactly two emissions interpolate
      // anything, and both interpolate server-controlled values -- the six
      // literal `action` strings at respondForLaunchError's call sites, and an
      // err.Error() that goes into `details` (already serialized) rather than
      // into the message.
      //
      // The status STAYS. Unlike the 403 and 404 arms, this is the one place
      // where the status is itself diagnostic -- 503 says "try later, Docker is
      // down", 500 says "this is a bug" -- so the cause is added to what was
      // shown before rather than swapped for it, and no caller loses anything.
      message: `${status}: ${backendMessage ?? 'Something went wrong on the server'}`,
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

/**
 * The cause the rejection itself carried, or null when it carried none.
 *
 * Exactly the chain classifyError builds its `backendMessage` local from —
 * extracted (agent-os-5g8a) so there is ONE implementation of "did the backend
 * say anything at all?". `null` is the load-bearing value: every classifyError
 * arm ends in a sentence, so `AppError.message` can never express silence.
 */
export function backendCauseOf(error: unknown): string | null {
  if (!error) return null
  const err = error as {
    response?: { data?: { error?: unknown; message?: unknown } }
    message?: unknown
  }
  return (
    messageOrNull(err.response?.data?.error) ??
    messageOrNull(err.response?.data?.message) ??
    messageOrNull(err.message)
  )
}

/**
 * What to tell the operator went wrong, or null when nothing is known.
 *
 * An ActionResult's own `reason` wins: it is the backend's considered diagnosis
 * of a multi-step write and names which step failed. Otherwise classifyError's
 * sentence, plus its `context` when it computed one (the 404/409/428 resource
 * and directory names, which are otherwise silently dropped by every consumer
 * that reads only `.message`).
 *
 * SUPPRESSION applies only when the backend said NOTHING, and then only to the
 * arms that have no diagnosis of their own. Two kinds of arm qualify:
 *
 *  - `type: 'unknown'` — exactly two of classifyError's 14 arms, the `!error`
 *    guard at the top and the terminal fallthrough at the bottom.
 *  - Arms whose message IS the no-backend-message sentinel. classifyError's
 *    400/422, 409 and 428 arms all spell `message || '<their own fallback>'`,
 *    and `message` is never falsy, so that fallback is dead code — the file's
 *    own comment says it "can never actually reach its fallback". A bodyless
 *    4xx (a proxy 4xx, the shape agent-os-ohkw exists to handle) therefore
 *    returns the literal 'An error occurred' under `type: 'server'` or
 *    `'validation'`, which the type key alone does not catch. Rendering that as
 *    a cause is a fixed sentence dressed as a diagnosis — the exact defect
 *    these presenters exist to end (found in review of agent-os-5g8a).
 *
 * Everything else survives, and must: 401 says 'Log in again to continue', 404
 * names the resource, 429 says to wait, the 5xx arm keeps the status because
 * the status is itself diagnostic (agent-os-mc4i). Those are informative even
 * when the body was empty.
 *
 * Keyed on a shared CONSTANT rather than on sentence text, so the rule and the
 * sentence it keys on cannot drift into two literals.
 */
export function causeOf(error: unknown): string | null {
  if (isActionResult(error) && error.reason) return error.reason
  const app = classifyError(error)
  if (backendCauseOf(error) === null && !hasOwnDiagnosis(app)) return null
  return app.context ? `${app.message} (${app.context})` : app.message
}

/** Whether this arm authored a sentence of its own, rather than falling back. */
function hasOwnDiagnosis(app: AppError): boolean {
  return app.type !== 'unknown' && app.message !== NO_BACKEND_MESSAGE
}

/** The sonner options these presenters pass through. Deliberately tiny. */
type ToastOptions = { id?: string | number; duration?: number }

/**
 * Render a failed action from an ALREADY-COMPUTED cause: the action context as
 * the toast TITLE, the cause as the DESCRIPTION.
 *
 * Both, never one instead of the other. The fixed sentence a call site passes
 * is the only place the ACTION lives ("Failed to extract variable to .env"),
 * and the cause is the only place the diagnosis lives ("failed to write compose
 * file; env rolled back"). Collapsing to a single line deletes one of them,
 * which is the defect class these helpers exist to end.
 *
 * SEPARATE from presentError because several forms read their cause with a
 * CODE-KEYED reader (settingsSaveFault, credentialSaveFault, updateScanFault,
 * repoFaultFrom) rather than with causeOf. Each of those exists precisely so as
 * NOT to be classifyError, and each says so in its own docblock (agent-os-zlw0).
 * Those call sites hand their already-read answer here. Three reasons, and the
 * first is the one that decides it:
 *
 *  1. Switching a reader's KEY is a decision this change is not entitled to
 *     take. The code-keying was chosen deliberately, is documented, and is
 *     pinned by tests.
 *  2. There IS a leak, but a narrower one than "axios's own message reaches the
 *     operator". MEASURED, not assumed: classifyError's first two arms are
 *     themselves code-keyed (ECONNABORTED/ETIMEDOUT, ERR_NETWORK) and the
 *     interceptor preserves `error.code`, and a later arm matches the substring
 *     'network' — so a genuine axios network error or timeout classifies
 *     correctly and does NOT leak. What leaks is the tail where axios's code is
 *     neither of those and its message matches none of the substring arms:
 *     `{code:'UNKNOWN', message:'timeout of 30000ms exceeded'}`,
 *     `{code:'UNKNOWN', message:'Request aborted'}` and
 *     `{code:'ERR_CANCELED', message:'canceled'}` all come back from causeOf
 *     verbatim. The code-keyed readers decline every one of them.
 *  3. COVERAGE, which cuts the other way and is the half that is easy to miss:
 *     code-keying PICKS UP faults a status-keyed reader drops. The backup
 *     endpoint mints VALIDATION_ERROR at 422, not 400, so its two most
 *     operator-actionable sentences would never render behind a status key.
 *
 * (An earlier revision of this comment claimed the leak was "Network Error"
 * itself. That was false and shipped in six copies; the repo's own
 * apiInterceptorError tests disprove it. Corrected in review.)
 *
 * `cause !== title` guards the degenerate case where they are the same string.
 * The one- vs two-argument split is load-bearing: sonner renders
 * `toast.error(t)` and `toast.error(t, undefined)` identically but a vitest spy
 * does not, and several tests pin the single-argument shape.
 *
 * An EMPTY title is substituted rather than rendered. `string` does not exclude
 * `''`, so "non-empty by type" was a false claim (found in review) -- the
 * promise is kept here, at runtime, or it is not kept at all. If only the cause
 * is known it becomes the title, which is better than a blank toast.
 */
export function presentFault(rawTitle: string, cause: string | null, extra?: ToastOptions): void {
  const title = rawTitle || cause || 'An unexpected error occurred'
  const description = cause && cause !== title ? cause : undefined
  if (description !== undefined) {
    toast.error(title, extra ? { ...extra, description } : { description })
    return
  }
  if (extra) {
    toast.error(title, extra)
    return
  }
  toast.error(title)
}

/**
 * Render a failed action whose cause has to be dug out of the rejection.
 *
 * The common case, and the one the eslint rule points every fixed-sentence
 * `toast.error` at. `fallback` is the toast TITLE and is always rendered.
 *
 * There is deliberately no separate `title` option. It would mean the same
 * thing as `fallback`, so passing both left one of them dead at the call site
 * -- which is what happened at the one site that used it (found in review).
 */
export function presentError(
  err: unknown,
  opts: { fallback: string; extra?: ToastOptions },
): void {
  presentFault(opts.fallback, causeOf(err), opts.extra)
}

/**
 * Render a rejection that has NO action context of its own: the cause IS the
 * message.
 *
 * For generic wrappers (useActionMutation) that do not know which action
 * failed, so there is no title to carry and a fixed one would be a lie.
 */
export function presentCause(err: unknown): void {
  toast.error(causeOf(err) ?? 'An unexpected error occurred')
}

/**
 * Render a message that is ALREADY FINAL — nothing further is to be looked up.
 *
 * Three kinds of site land here, and they share the property that asking
 * causeOf would be wrong rather than merely redundant:
 *
 *  - CLIENT-SIDE refusals and local-state notices, where no rejection exists
 *    anywhere in scope ("Passwords do not match", a clipboard write that
 *    failed, an inactivity disconnect). Routing these through classifyError is
 *    actively harmful: its `!navigator.onLine` arm rewrites ANY error to "Check
 *    your connection and try again", so an offline operator would be told their
 *    clipboard failure was a network problem.
 *  - Sites where a CODE-KEYED reader has already looked and declined, so the
 *    fixed sentence is a deliberate refusal to claim a cause.
 *  - The `cause || 'Generic sentence'` shape, where the cause is already
 *    resolved and belongs in the TITLE — the same call toastForResult's `failed`
 *    arm makes.
 *
 * Named rather than inlined so the distinction is visible at the call site and
 * greppable later: the INLINE-vs-TOAST convention at the top of this file says
 * many of these belong next to the field the user is looking at, not in a
 * toast. Converting them is a separate job; this marks the set.
 */
export function toastInvalid(message: string, extra?: ToastOptions): void {
  presentFault(message, null, extra)
}
