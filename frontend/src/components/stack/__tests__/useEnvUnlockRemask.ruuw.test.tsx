import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useState } from 'react'
import { useEnvUnlockRemask } from '../env-editor/useEnvUnlockRemask'
import type { EnvEntryRow } from '../env-editor/types'

// agent-os-ruuw (notes): a reveal is stored as `sensitive: false` on the row,
// and a lock re-masked only rows whose KEY looks secret. A row the server
// flagged for its value (a private key under TLS) stayed revealed after the
// lock.

const ROWS: EnvEntryRow[] = [
  { key: 'TLS', value: '"-----BEGIN PRIVATE KEY-----"', sensitive: true, _rowId: 1 },
  { key: 'DB_PASSWORD', value: 'pw', sensitive: true, _rowId: 2 },
  { key: 'TZ', value: 'UTC', sensitive: false, _rowId: 3 },
]

function useHarness(unlockedUntil: number | null) {
  const [entries, setEntries] = useState<EnvEntryRow[]>(ROWS)
  useEnvUnlockRemask(unlockedUntil, setEntries, entries)
  return { entries, setEntries }
}

describe('useEnvUnlockRemask (agent-os-ruuw)', () => {
  it('re-masks a revealed row the server flagged, whatever its key, when the unlock ends', () => {
    const { result, rerender } = renderHook(({ until }) => useHarness(until), {
      initialProps: { until: 1000 as number | null },
    })
    // The user reveals every row.
    act(() => result.current.setEntries((prev) => prev.map((e) => ({ ...e, sensitive: false }))))
    expect(result.current.entries.map((e) => e.sensitive)).toEqual([false, false, false])

    rerender({ until: null })

    expect(result.current.entries.map((e) => [e.key, e.sensitive])).toEqual([
      ['TLS', true],
      ['DB_PASSWORD', true],
      ['TZ', false],
    ])
  })
})
