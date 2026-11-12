import { describe, it, expect } from 'vitest'
import { fillRedactedBlanks } from '../env-unlock-rebase'

const redacted = new Set(['API_KEY'])
const row = (_rowId: number, key: string, value: string) => ({ _rowId, key, value })

describe('fillRedactedBlanks (agent-os-vqf5)', () => {
  it('vqf5: a kept row that only holds the redacted blank takes the real value', () => {
    const baseline = [row(1, 'PORT', '8080'), row(2, 'API_KEY', '')]
    const kept = [row(1, 'PORT', '9090'), row(2, 'API_KEY', '')]
    const next = [{ key: 'PORT', value: '8080' }, { key: 'API_KEY', value: 'real' }]

    expect(fillRedactedBlanks(kept, baseline, redacted, next)).toEqual([
      row(1, 'PORT', '9090'),
      row(2, 'API_KEY', 'real'),
    ])
  })

  it('vqf5: a secret the user cleared on purpose stays cleared (the baseline held the real value)', () => {
    const baseline = [row(1, 'API_KEY', 'real')]
    const kept = [row(1, 'API_KEY', '')]

    expect(fillRedactedBlanks(kept, baseline, redacted, [{ key: 'API_KEY', value: 'real' }])).toEqual(kept)
  })

  it('vqf5: a typed value is never replaced', () => {
    const baseline = [row(1, 'API_KEY', '')]
    const kept = [row(1, 'API_KEY', 'typed')]

    expect(fillRedactedBlanks(kept, baseline, redacted, [{ key: 'API_KEY', value: 'real' }])).toEqual(kept)
  })

  it('vqf5: a blank on a key the locked payload did not redact is left alone', () => {
    const baseline = [row(1, 'EMPTY', '')]

    expect(fillRedactedBlanks(baseline, baseline, redacted, [{ key: 'EMPTY', value: 'now set' }])).toEqual(baseline)
  })

  it('vqf5: a renamed row keeps its identity and takes its own secret', () => {
    const baseline = [row(1, 'API_KEY', '')]
    const kept = [row(1, 'API_KEY_V2', '')]

    expect(fillRedactedBlanks(kept, baseline, redacted, [{ key: 'API_KEY', value: 'real' }])).toEqual([
      row(1, 'API_KEY_V2', 'real'),
    ])
  })

  it('vqf5: duplicate keys match by the baseline row, not by position in the kept rows', () => {
    const baseline = [row(1, 'API_KEY', ''), row(2, 'API_KEY', '')]
    const kept = [row(2, 'API_KEY', '')] // the first one was deleted
    const next = [{ key: 'API_KEY', value: 'one' }, { key: 'API_KEY', value: 'two' }]

    expect(fillRedactedBlanks(kept, baseline, redacted, next)).toEqual([row(2, 'API_KEY', 'two')])
  })

  it('vqf5: a row with no baseline row (added while locked) takes nothing', () => {
    const baseline = [row(1, 'API_KEY', '')]
    const kept = [row(9, 'API_KEY', ''), row(1, 'API_KEY', '')]

    expect(fillRedactedBlanks(kept, baseline, redacted, [{ key: 'API_KEY', value: 'real' }])).toEqual([
      row(9, 'API_KEY', ''),
      row(1, 'API_KEY', 'real'),
    ])
  })

  it('vqf5: returns the same array when nothing needed filling', () => {
    const kept = [row(1, 'PORT', '9090')]

    expect(fillRedactedBlanks(kept, kept, redacted, kept)).toBe(kept)
  })
})
