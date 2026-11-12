import { describe, it, expect } from 'vitest'
import { fillRedactedBlanks } from '../env-unlock-rebase'

const redacted = new Set(['API_KEY'])

describe('fillRedactedBlanks (agent-os-vqf5)', () => {
  it('vqf5: a kept row that only holds the redacted blank takes the real value', () => {
    const kept = [{ key: 'PORT', value: '9090' }, { key: 'API_KEY', value: '' }]
    const baseline = [{ key: 'PORT', value: '8080' }, { key: 'API_KEY', value: '' }]
    const next = [{ key: 'PORT', value: '8080' }, { key: 'API_KEY', value: 'real' }]

    expect(fillRedactedBlanks(kept, baseline, redacted, next)).toEqual([
      { key: 'PORT', value: '9090' },
      { key: 'API_KEY', value: 'real' },
    ])
  })

  it('vqf5: a secret the user cleared on purpose stays cleared (the baseline held the real value)', () => {
    const kept = [{ key: 'API_KEY', value: '' }]
    const baseline = [{ key: 'API_KEY', value: 'real' }]
    const next = [{ key: 'API_KEY', value: 'real' }]

    expect(fillRedactedBlanks(kept, baseline, redacted, next)).toEqual([{ key: 'API_KEY', value: '' }])
  })

  it('vqf5: a typed value is never replaced', () => {
    const kept = [{ key: 'API_KEY', value: 'typed' }]
    const baseline = [{ key: 'API_KEY', value: '' }]
    const next = [{ key: 'API_KEY', value: 'real' }]

    expect(fillRedactedBlanks(kept, baseline, redacted, next)).toEqual([{ key: 'API_KEY', value: 'typed' }])
  })

  it('vqf5: a blank on a key the locked payload did not redact is left alone', () => {
    const kept = [{ key: 'EMPTY', value: '' }]
    const next = [{ key: 'EMPTY', value: 'now set' }]

    expect(fillRedactedBlanks(kept, kept, redacted, next)).toEqual([{ key: 'EMPTY', value: '' }])
  })

  it('vqf5: duplicate keys match by occurrence', () => {
    const kept = [{ key: 'API_KEY', value: '' }, { key: 'API_KEY', value: '' }]
    const next = [{ key: 'API_KEY', value: 'one' }, { key: 'API_KEY', value: 'two' }]

    expect(fillRedactedBlanks(kept, kept, redacted, next).map((r) => r.value)).toEqual(['one', 'two'])
  })

  it('vqf5: returns the same array when nothing needed filling', () => {
    const kept = [{ key: 'PORT', value: '9090' }]

    expect(fillRedactedBlanks(kept, kept, redacted, kept)).toBe(kept)
  })
})
