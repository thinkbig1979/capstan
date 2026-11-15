/**
 * agent-os-uttl item 4: `lockedNow` is required. While it defaulted to false, a
 * new editor that forgot it opted out of dropping a version served unlocked at
 * a lock, silently. The check is the type checker (`npx tsc -b`): the directive
 * below is an error the day the option becomes optional again.
 */
import { describe, it, expect } from 'vitest'
import type { useExternalFileChange } from '../useExternalFileChange'

type Options = Parameters<typeof useExternalFileChange<string>>[1]

describe('useExternalFileChange options (agent-os-uttl item 4)', () => {
  it('lockedNow is required', () => {
    // @ts-expect-error lockedNow is required: a caller must say whether env values are locked now
    const missing: Options = { isDirty: false, hydrate: () => {} }
    const given: Options = { isDirty: false, hydrate: () => {}, lockedNow: false }
    expect([missing, given]).toHaveLength(2)
  })
})
