import { describe, it, expect } from 'vitest'
import { settingsSaveFault } from '../settings-save-fault'

/**
 * agent-os-zlw0. The four settings-save forms used to pass a ZERO-ARITY
 * `onError: () => {...}` to React Query, so the sentence naming which field the
 * backend rejected could not be read even in principle. This reader is what
 * they call instead, and the case that decides its shape is the NETWORK one
 * below: no existing test in this repo had ever fed a network-shaped error to a
 * toast-cause helper, and a reader written as `err?.message ?? null` passes tsc
 * and vitest green while rendering axios's "Network Error" to the operator as
 * though the backend had said it.
 */

// The shape lib/api.ts:131 rejects with when there is no HTTP response at all.
// Copied from lib/__tests__/query-client.test.ts:21 rather than invented, so
// this arm tests the shape the interceptor really produces.
const NETWORK_ERROR = { error: 'Unknown error', code: 'ERR_NETWORK', message: 'Network Error' }

describe('settingsSaveFault', () => {
  it('returns the message on a VALIDATION_ERROR', () => {
    expect(
      settingsSaveFault({
        error: 'Bad Request',
        code: 'VALIDATION_ERROR',
        message: 'Scan interval must be 0 (disabled) or at least 15 minutes',
        status: 400,
      }),
    ).toBe('Scan interval must be 0 (disabled) or at least 15 minutes')
  })

  it('returns the message on an ENCRYPTION_KEY_MISSING', () => {
    const message =
      'Cannot store this value: no encryption key is configured. Set STORAGE_KEY (or JWT_SECRET) in the environment and restart Capstan, then try again.'
    expect(
      settingsSaveFault({ error: 'Unprocessable Entity', code: 'ENCRYPTION_KEY_MISSING', message, status: 422 }),
    ).toBe(message)
  })

  // VALIDATION_ERROR arrives at 422 as well as 400 on the backup endpoint
  // (backup.go:449 and :462). Keyed on the code, so the status is irrelevant —
  // pinned here because a status-keyed reader would drop exactly these two.
  it('does not care which status carried the code', () => {
    expect(
      settingsSaveFault({
        code: 'VALIDATION_ERROR',
        message: 'The repository value still contains the redacted credential marker (***).',
        status: 422,
      }),
    ).toBe('The repository value still contains the redacted credential marker (***).')
  })

  it('returns null for a network failure, whose message is axios internals', () => {
    expect(settingsSaveFault(NETWORK_ERROR)).toBeNull()
  })

  it('returns null for a timeout, same reason', () => {
    expect(
      settingsSaveFault({
        error: 'Unknown error',
        code: 'ECONNABORTED',
        message: 'timeout of 30000ms exceeded',
      }),
    ).toBeNull()
  })

  it('returns null for undefined', () => {
    expect(settingsSaveFault(undefined)).toBeNull()
  })

  it('returns null for null and for a non-object', () => {
    expect(settingsSaveFault(null)).toBeNull()
    expect(settingsSaveFault('Failed')).toBeNull()
  })

  // An allow-listed code with nothing to say is not a description. Returning ''
  // would render an empty line under the title instead of the title alone.
  it('returns null when an allow-listed code carries no message', () => {
    expect(settingsSaveFault({ code: 'VALIDATION_ERROR', status: 400 })).toBeNull()
    expect(settingsSaveFault({ code: 'ENCRYPTION_KEY_MISSING', status: 422 })).toBeNull()
  })

  it('returns null when an allow-listed code carries an empty message', () => {
    expect(settingsSaveFault({ code: 'VALIDATION_ERROR', message: '', status: 400 })).toBeNull()
  })

  it('returns null when the message is not a string', () => {
    expect(settingsSaveFault({ code: 'VALIDATION_ERROR', message: { detail: 'nope' } })).toBeNull()
  })

  // The allow-list is closed. INTERNAL_ERROR and the middleware codes carry a
  // message too, and every one of them falls through to the caller's own
  // generic sentence.
  it('returns null for a code outside the allow-list, message or not', () => {
    expect(settingsSaveFault({ code: 'INTERNAL_ERROR', message: 'Failed to update scan interval' })).toBeNull()
    expect(settingsSaveFault({ code: 'SESSION_EXPIRED', message: 'Your session has expired' })).toBeNull()
    expect(settingsSaveFault({ code: 'NOT_FOUND', message: 'Directory not found' })).toBeNull()
    expect(settingsSaveFault({ message: 'no code at all' })).toBeNull()
  })

  // An Error instance is an object with a `message` and no `code`. React Query
  // hands one over on any non-axios throw, and it must not become a description.
  it('returns null for a plain Error', () => {
    expect(settingsSaveFault(new Error('boom'))).toBeNull()
  })
})
