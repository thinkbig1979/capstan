import { describe, it, expect } from 'vitest'
import { messageOrNull, stringArrayOr, stringOr } from '../narrow'

/**
 * agent-os-06c1 criterion 3: the empty-string question is DECIDED here rather
 * than inherited, and these are the tests that pin the decision.
 *
 * Before this change the fault readers wrote `body.message || null`, so a
 * `message: ''` collapsed to null. That is KEPT, deliberately: an empty server
 * sentence is not a usable one and every caller falls back to its own generic
 * copy, which is better copy than a blank description. Switching to `??` would
 * have surfaced an empty toast description instead, and nothing pinned the
 * difference — so it is pinned now, in both directions.
 *
 * repoFaultFrom used `?? ''` rather than `|| ''` and keeps doing so. It reaches
 * the same place by a different route (empty in, empty out) so there was never
 * a disagreement to resolve, only two spellings of one behaviour.
 */
describe('messageOrNull', () => {
  it('keeps a real sentence', () => {
    expect(messageOrNull('Docker daemon unreachable')).toBe('Docker daemon unreachable')
  })

  it('collapses the empty string to null, preserving the `|| null` it replaced', () => {
    expect(messageOrNull('')).toBeNull()
  })

  it('rejects every non-string, which is the whole point', () => {
    expect(messageOrNull({ a: 1 })).toBeNull()
    expect(messageOrNull(42)).toBeNull()
    expect(messageOrNull(['a'])).toBeNull()
    expect(messageOrNull(true)).toBeNull()
    expect(messageOrNull(null)).toBeNull()
    expect(messageOrNull(undefined)).toBeNull()
  })

  // The two-sided arm. `||` and `??` differ on EXACTLY one input, so a test
  // that only feeds a real sentence cannot tell which one shipped.
  it('differs from a `??`-shaped helper only on the empty string', () => {
    const nullish = (v: unknown) => (typeof v === 'string' ? v : null)
    for (const v of ['x', {}, 42, null, undefined]) {
      expect(messageOrNull(v)).toEqual(nullish(v))
    }
    expect(messageOrNull('')).toBeNull()
    expect(nullish('')).toBe('')
  })
})

describe('stringOr', () => {
  it('keeps the empty string, unlike messageOrNull', () => {
    // Its callers used `?? ''` / `?? 'unknown'`: they distinguish ABSENT from
    // empty, and only absent takes the fallback.
    expect(stringOr('', 'unknown')).toBe('')
    expect(stringOr(undefined, 'unknown')).toBe('unknown')
    expect(stringOr({ a: 1 }, 'unknown')).toBe('unknown')
    expect(stringOr('bridge-1', 'unknown')).toBe('bridge-1')
  })
})

describe('stringArrayOr', () => {
  it('accepts an all-string array and the empty array', () => {
    expect(stringArrayOr(['.git', 'data/'], [])).toEqual(['.git', 'data/'])
    expect(stringArrayOr([], ['fallback'])).toEqual([])
  })

  it('rejects the whole list when any member is not a string', () => {
    // All-or-nothing on purpose: these lists are shown to a user as "everything
    // that will be affected", so dropping the members that failed the check
    // would UNDER-report the blast radius of a destructive delete.
    expect(stringArrayOr(['.git', { name: 'data' }], [])).toEqual([])
    expect(stringArrayOr('not-an-array', [])).toEqual([])
    expect(stringArrayOr(undefined, [])).toEqual([])
  })
})
