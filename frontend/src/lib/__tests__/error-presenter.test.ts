import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  backendCauseOf,
  causeOf,
  classifyError,
  presentError,
  presentFault,
  presentCause,
  toastInvalid,
} from '../error-handler'

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
  },
}))

// Re-import after mock is in place so we get the mocked references.
import { toast } from 'sonner'

beforeEach(() => {
  vi.clearAllMocks()
})

// ─── backendCauseOf ──────────────────────────────────────────────────────────

describe('backendCauseOf', () => {
  it('returns null when the rejection carried no message at all', () => {
    expect(backendCauseOf({ status: 500 })).toBeNull()
  })

  it('returns null for a falsy rejection', () => {
    expect(backendCauseOf(null)).toBeNull()
    expect(backendCauseOf(undefined)).toBeNull()
  })

  it('reads response.data.error first', () => {
    expect(
      backendCauseOf({ response: { data: { error: 'disk full', message: 'other' } }, message: 'axios' }),
    ).toBe('disk full')
  })

  it('falls through to response.data.message', () => {
    expect(backendCauseOf({ response: { data: { message: 'port in use' } } })).toBe('port in use')
  })

  it('falls through to the top-level message', () => {
    expect(backendCauseOf({ message: 'Network Error' })).toBe('Network Error')
  })

  it('treats an empty string as no message, not as a message', () => {
    expect(backendCauseOf({ response: { data: { error: '' } }, message: 'fallback' })).toBe('fallback')
  })

  it('rejects a non-string message rather than passing it through', () => {
    expect(backendCauseOf({ message: { nested: true } })).toBeNull()
  })
})

// ─── causeOf ─────────────────────────────────────────────────────────────────

describe('causeOf', () => {
  it('prefers an ActionResult reason over anything classifyError would say', () => {
    const r = { outcome: 'failed', reason: 'failed to write compose file; env rolled back' }
    expect(causeOf(r)).toBe('failed to write compose file; env rolled back')
  })

  it('ignores an ActionResult with an empty reason', () => {
    const r = { outcome: 'failed', reason: '', message: 'transport said this' }
    expect(causeOf(r)).toBe('transport said this')
  })

  it('returns null when nothing is known: no diagnosis and no backend message', () => {
    expect(causeOf(null)).toBeNull()
    expect(causeOf({})).toBeNull()
  })

  it('keeps a diagnosing arm even when the backend said nothing', () => {
    // A timeout carries its own advice; suppressing it because the body was
    // empty would delete the only useful thing on screen.
    expect(causeOf({ code: 'ECONNABORTED' })).toBe('The request timed out. Please try again.')
  })

  it('returns the backend message from the terminal fallthrough arm', () => {
    expect(causeOf({ message: 'something specific' })).toBe('something specific')
  })

  // agent-os-5g8a, defect found in review. classifyError's 400/409/422/428 arms
  // all spell `message || '<their own fallback>'`, and `message` is
  // `backendMessage ?? 'An error occurred'` -- never falsy. The file's own
  // comment says that fallback "can never actually reach its fallback", so a
  // BODYLESS 4xx (a proxy 4xx, exactly the shape agent-os-ohkw exists to
  // handle) returns the literal 'An error occurred' with type 'server' or
  // 'validation'. The `type === 'unknown'` key does not catch it, so presentError
  // rendered a description reading "An error occurred": a fixed sentence dressed
  // as a cause, in the change whose whole purpose is to stop that.
  it.each([400, 409, 422, 428])('returns null for a bodyless %i', (status) => {
    expect(causeOf({ status })).toBeNull()
  })

  // DRIFT GUARD. The predicate above and classifyError share ONE module constant,
  // so they cannot drift apart today. This arm fails loudly if a future refactor
  // reintroduces two copies of the sentence: it pins the observable pair rather
  // than the constant, so it goes red if either side moves alone.
  it('pins the sentence the suppression is keyed on', () => {
    expect(classifyError({ status: 409 }).message).toBe('An error occurred')
    expect(classifyError({ status: 409 }).type).toBe('server')
    expect(causeOf({ status: 409 })).toBeNull()
  })

  // TWO-SIDED, and this is the half that stops the fix over-reaching: an arm
  // that authored a real sentence of its own must still reach the user even
  // though the backend said nothing.
  it.each([
    [401, 'Log in again to continue'],
    [403, 'You do not have permission to perform this action'],
    [404, 'The requested resource was not found'],
    [429, 'Too many requests. Please wait a moment and try again'],
    [500, '500: Something went wrong on the server'],
    [503, '503: Something went wrong on the server'],
  ])('keeps the self-authored diagnosis for a bodyless %i', (status, expected) => {
    expect(causeOf({ status })).toBe(expected)
  })

  // And a body always wins: suppression is keyed on the backend having said
  // NOTHING, never on the status alone.
  it('returns the backend message for a 409 that carried one', () => {
    expect(causeOf({ status: 409, message: 'stack is already being modified' }))
      .toBe('stack is already being modified')
  })

  it('appends classifyError context when it computed one', () => {
    const cause = causeOf({ status: 404, details: { resource: 'stack/web' } })
    expect(cause).toBe('The requested resource was not found (stack/web)')
  })
})

// ─── presentError ────────────────────────────────────────────────────────────

describe('presentError', () => {
  it('renders the fallback as TITLE and the cause as DESCRIPTION', () => {
    presentError({ response: { data: { error: 'permission denied' } }, status: 500 }, {
      fallback: 'Failed to save settings',
    })
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings', {
      description: '500: permission denied',
    })
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('renders a single-argument toast when no cause is known', () => {
    presentError({}, { fallback: 'Failed to save settings' })
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings')
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('does not repeat itself when the cause equals the title', () => {
    presentError({ message: 'Failed to save settings' }, { fallback: 'Failed to save settings' })
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings')
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('carries an ActionResult reason into the description', () => {
    presentError(
      { outcome: 'failed', reason: 'failed to write env file; compose unchanged' },
      { fallback: 'Failed to extract variable to .env' },
    )
    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'failed to write env file; compose unchanged',
    })
  })

  // agent-os-5g8a, found in review: the docblock claimed `fallback` was
  // "non-empty by type", but `string` does not exclude `''`, so the
  // never-an-empty-toast promise rested on nothing. It is enforced at runtime
  // now and these two arms are what hold it there.
  it('promotes the cause to the title when the fallback is empty', () => {
    presentError({ message: 'disk full' }, { fallback: '' })
    expect(toast.error).toHaveBeenCalledWith('disk full')
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('never renders an empty toast when both the fallback and the cause are empty', () => {
    presentError({}, { fallback: '' })
    expect(toast.error).toHaveBeenCalledWith('An unexpected error occurred')
  })

  it('never fires a level other than error', () => {
    presentError({ message: 'nope' }, { fallback: 'Failed to X' })
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.info).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
  })
})

// ─── presentFault ────────────────────────────────────────────────────────────

describe('presentFault', () => {
  it('renders a single-argument toast when the cause is null', () => {
    presentFault('Failed to save settings', null)
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings')
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('renders the cause as a description when there is one', () => {
    presentFault('Failed to save settings', 'scanIntervalMinutes must be at least 15')
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings', {
      description: 'scanIntervalMinutes must be at least 15',
    })
  })

  it('merges extra options with the description', () => {
    presentFault('Update check failed', 'Docker is not running', { id: 'update-scan', duration: 4000 })
    expect(toast.error).toHaveBeenCalledWith('Update check failed', {
      id: 'update-scan',
      duration: 4000,
      description: 'Docker is not running',
    })
  })

  it('passes extra options through on the no-cause path, still without a description', () => {
    presentFault('Update check failed', null, { id: 'update-scan', duration: 4000 })
    expect(toast.error).toHaveBeenCalledWith('Update check failed', {
      id: 'update-scan',
      duration: 4000,
    })
  })

  it('does not repeat itself when the cause equals the title', () => {
    presentFault('Failed to save settings', 'Failed to save settings')
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings')
  })

  it('treats an empty cause as no cause', () => {
    presentFault('Failed to save settings', '')
    expect(toast.error).toHaveBeenCalledWith('Failed to save settings')
  })
})

// ─── presentCause ────────────────────────────────────────────────────────────

describe('presentCause', () => {
  it('puts the cause in the TITLE, one argument, no description', () => {
    presentCause({ response: { data: { error: 'Docker daemon is not running' } } })
    expect(toast.error).toHaveBeenCalledWith('Docker daemon is not running')
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('falls back to a generic sentence when nothing is known', () => {
    presentCause({})
    expect(toast.error).toHaveBeenCalledWith('An unexpected error occurred')
  })
})

// ─── toastInvalid ────────────────────────────────────────────────────────────

describe('toastInvalid', () => {
  it('renders the fixed sentence as a single-argument error toast', () => {
    toastInvalid('Custom interval must be at least 15 minutes')
    expect(toast.error).toHaveBeenCalledWith('Custom interval must be at least 15 minutes')
    expect(toast.error).toHaveBeenCalledTimes(1)
  })
})
