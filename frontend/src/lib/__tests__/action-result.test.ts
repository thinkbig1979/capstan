import { describe, it, expect, vi, beforeEach } from 'vitest'
import { toastForResult, isActionResult, type ActionResult } from '../action-result'

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

// ─── toastForResult ──────────────────────────────────────────────────────────

describe('toastForResult', () => {
  it('calls toast.success for outcome=success using result reason', () => {
    const r: ActionResult = { outcome: 'success', reason: 'Stack started' }
    toastForResult(r)
    expect(toast.success).toHaveBeenCalledWith('Stack started')
    expect(toast.info).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('uses successTitle override when provided for success outcome', () => {
    const r: ActionResult = { outcome: 'success', reason: 'Done' }
    toastForResult(r, { successTitle: 'Update applied' })
    expect(toast.success).toHaveBeenCalledWith('Update applied')
  })

  it('calls toast.info for outcome=no_change using result reason', () => {
    const r: ActionResult = { outcome: 'no_change', reason: 'Image already up to date' }
    toastForResult(r)
    expect(toast.info).toHaveBeenCalledWith('Image already up to date')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('falls back to "Already up to date" when no_change reason is empty', () => {
    const r: ActionResult = { outcome: 'no_change', reason: '' }
    toastForResult(r)
    expect(toast.info).toHaveBeenCalledWith('Already up to date')
  })

  it('calls toast.warning for outcome=partial', () => {
    const r: ActionResult = { outcome: 'partial', reason: '2 of 3 services updated' }
    toastForResult(r)
    expect(toast.warning).toHaveBeenCalledWith('2 of 3 services updated')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('calls toast.error for outcome=failed', () => {
    const r: ActionResult = { outcome: 'failed', reason: 'Pull failed: manifest unknown' }
    toastForResult(r)
    expect(toast.error).toHaveBeenCalledWith('Pull failed: manifest unknown')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('successTitle has no effect for non-success outcomes', () => {
    const r: ActionResult = { outcome: 'failed', reason: 'Something went wrong' }
    toastForResult(r, { successTitle: 'Should not appear' })
    expect(toast.error).toHaveBeenCalledWith('Something went wrong')
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// ─── isActionResult ──────────────────────────────────────────────────────────

describe('isActionResult', () => {
  it('returns true for a valid success result', () => {
    expect(isActionResult({ outcome: 'success', reason: 'ok' })).toBe(true)
  })

  it('returns true for no_change', () => {
    expect(isActionResult({ outcome: 'no_change', reason: 'no change' })).toBe(true)
  })

  it('returns true for partial', () => {
    expect(isActionResult({ outcome: 'partial', reason: 'partial' })).toBe(true)
  })

  it('returns true for failed', () => {
    expect(isActionResult({ outcome: 'failed', reason: 'err' })).toBe(true)
  })

  it('returns false for null', () => {
    expect(isActionResult(null)).toBe(false)
  })

  it('returns false for a string', () => {
    expect(isActionResult('success')).toBe(false)
  })

  it('returns false when outcome field is absent', () => {
    expect(isActionResult({ reason: 'ok' })).toBe(false)
  })

  it('returns false when outcome is an unrecognised string', () => {
    expect(isActionResult({ outcome: 'unknown', reason: 'x' })).toBe(false)
  })

  it('returns false for a legacy boolean-success response', () => {
    expect(isActionResult({ status: 'ok', message: 'done' })).toBe(false)
  })
})
