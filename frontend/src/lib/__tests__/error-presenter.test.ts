import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  backendCauseOf,
  causeOf,
  presentError,
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

  it('honours an explicit title override', () => {
    presentError({ message: 'nope' }, { fallback: 'Failed to X', title: 'Could not X' })
    expect(toast.error).toHaveBeenCalledWith('Could not X', { description: 'nope' })
  })

  it('never fires a level other than error', () => {
    presentError({ message: 'nope' }, { fallback: 'Failed to X' })
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.info).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
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
