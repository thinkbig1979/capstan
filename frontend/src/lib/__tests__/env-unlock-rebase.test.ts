import { describe, it, expect } from 'vitest'
import { alignRedactedRows, refillRedactedRows, NO_REDACTED_ROWS } from '../env-unlock-rebase'

type Row = { _rowId: number; key: string; value: string; line?: number; comment?: boolean }
const row = (_rowId: number, key: string, value: string, extra: Partial<Row> = {}): Row => ({ _rowId, key, value, ...extra })
const FIELDS = ['key', 'value', 'comment'] as const

/** Aligns and refills in one step; null when the payloads do not line up. */
function refill(kept: Row[], baseline: Row[], redacted: number[], next: Array<Omit<Row, '_rowId'>>) {
  const aligned = alignRedactedRows(baseline, new Set(redacted), next)
  return aligned && refillRedactedRows(kept, baseline, aligned, FIELDS)
}

describe('refillRedactedRows (agent-os-vqf5)', () => {
  it('vqf5: a kept row that only holds the redacted blank takes the real value', () => {
    const baseline = [row(1, 'PORT', '8080'), row(2, 'API_KEY', '')]
    const kept = [row(1, 'PORT', '9090'), row(2, 'API_KEY', '')]
    const next = [{ key: 'PORT', value: '8080' }, { key: 'API_KEY', value: 'real' }]

    expect(refill(kept, baseline, [2], next)).toEqual([row(1, 'PORT', '9090'), row(2, 'API_KEY', 'real')])
  })

  it('vqf5: a typed value is never replaced', () => {
    const baseline = [row(1, 'API_KEY', '')]
    const kept = [row(1, 'API_KEY', 'typed')]

    expect(refill(kept, baseline, [1], [{ key: 'API_KEY', value: 'real' }])).toEqual(kept)
  })

  it('vqf5: a row the locked payload did not redact is left alone', () => {
    const baseline = [row(1, 'EMPTY', '')]

    expect(refill(baseline, baseline, [], [{ key: 'EMPTY', value: 'now set' }])).toEqual(baseline)
  })

  it('vqf5: a renamed row keeps its identity and takes its own secret', () => {
    const baseline = [row(1, 'API_KEY', '')]
    const kept = [row(1, 'API_KEY_V2', '')]

    expect(refill(kept, baseline, [1], [{ key: 'API_KEY', value: 'real' }])).toEqual([row(1, 'API_KEY_V2', 'real')])
  })

  it('vqf5: duplicate keys match by the baseline row, not by position in the kept rows', () => {
    const baseline = [row(1, 'API_KEY', ''), row(2, 'API_KEY', '')]
    const kept = [row(2, 'API_KEY', '')] // the first one was deleted
    const next = [{ key: 'API_KEY', value: 'one' }, { key: 'API_KEY', value: 'two' }]

    expect(refill(kept, baseline, [1, 2], next)).toEqual([row(2, 'API_KEY', 'two')])
  })

  it('vqf5: a row with no baseline row (added while locked) takes nothing', () => {
    const baseline = [row(1, 'API_KEY', '')]
    const kept = [row(9, 'API_KEY', ''), row(1, 'API_KEY', '')]

    expect(refill(kept, baseline, [1], [{ key: 'API_KEY', value: 'real' }])).toEqual([
      row(9, 'API_KEY', ''),
      row(1, 'API_KEY', 'real'),
    ])
  })

  it('vqf5: returns the same array when nothing needed filling', () => {
    const kept = [row(1, 'PORT', '9090')]

    expect(refill(kept, kept, [], kept)).toBe(kept)
  })
})

describe('refillRedactedRows, the locked shapes beyond a blank value (agent-os-yt2f)', () => {
  // The Go shapes: a commented secret cut to "#KEY=", a body row with key and value blank.
  const baseline = [
    row(1, 'PORT', '8080', { line: 1 }),
    row(2, '', '#OLD_SECRET=', { line: 2, comment: true }),
    row(3, 'SSH_KEY', '', { line: 3 }),
    row(4, '', '', { line: 4, comment: true }),
    row(5, 'TZ', 'UTC', { line: 5 }),
  ]
  const next = [
    { key: 'PORT', value: '8080', line: 1 },
    { key: '', value: '#OLD_SECRET=commentedvalue', line: 2, comment: true },
    { key: 'SSH_KEY', value: '"-----BEGIN', line: 3 },
    { key: 'bodylineAAAA', value: 'bodylineAAAA', line: 4, comment: true },
    { key: 'TZ', value: 'UTC', line: 5 },
  ]

  it('yt2f: an untouched commented secret and body row take the whole real row, key included', () => {
    expect(refill(baseline, baseline, [2, 3, 4], next)).toEqual(next.map((n, i) => ({ ...n, _rowId: i + 1 })))
  })

  it('yt2f: a field the user changed stays as typed while the others are filled', () => {
    const kept = [baseline[0], baseline[1], row(3, 'DEPLOY_KEY', '', { line: 3 }), baseline[3], baseline[4]]

    expect(refill(kept, baseline, [2, 3, 4], next)?.[2]).toEqual(row(3, 'DEPLOY_KEY', '"-----BEGIN', { line: 3 }))
  })
})

describe('alignRedactedRows fails closed (agent-os-yt2f)', () => {
  const baseline = [row(1, 'PORT', '8080', { line: 1 }), row(2, 'API_KEY', '', { line: 2 })]

  it('yt2f: lines up when count, lines, comment flags and the non-blank keys agree', () => {
    const next = [{ key: 'PORT', value: '9', line: 1 }, { key: 'API_KEY', value: 'real', line: 2 }]

    expect(alignRedactedRows(baseline, new Set([2]), next)).toEqual(new Map([[2, next[1]]]))
  })

  it.each([
    ['a row more', [{ key: 'PORT', value: '8080', line: 1 }, { key: 'API_KEY', value: 'r', line: 2 }, { key: 'NEW', value: '1', line: 3 }]],
    ['a row less', [{ key: 'PORT', value: '8080', line: 1 }]],
    ['a different key', [{ key: 'PORT', value: '8080', line: 1 }, { key: 'OTHER', value: 'r', line: 2 }]],
    ['a different line', [{ key: 'PORT', value: '8080', line: 1 }, { key: 'API_KEY', value: 'r', line: 3 }]],
    ['a different comment flag', [{ key: 'PORT', value: '8080', line: 1 }, { key: 'API_KEY', value: 'r', line: 2, comment: true }]],
  ])('yt2f: %s does not line up', (_name, next) => {
    expect(alignRedactedRows(baseline, new Set([2]), next)).toBeNull()
  })

  it('yt2f: with nothing redacted there is nothing to line up, whatever the payload', () => {
    expect(alignRedactedRows(baseline, NO_REDACTED_ROWS, [])).toEqual(new Map())
  })
})
