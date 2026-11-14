import { useState, type Dispatch, type SetStateAction } from 'react'
import { isSensitiveKey } from './sensitiveKey'
import type { EnvEntryRow } from './types'

const NO_ROWS: ReadonlySet<number> = new Set()

/**
 * When the unlock session ends (manual lock or auto-expiry), re-mask every
 * entry the user had revealed during the session: one whose key looks secret,
 * and one that was masked since it entered the editor, which covers a value
 * the server flagged under a harmless key (agent-os-ruuw). A reveal is stored
 * as `sensitive: false` on the row, so the rows ever masked are remembered by
 * `_rowId`.
 *
 * Adjusted during render (rather than in an effect) by comparing against
 * the previous render's unlockedUntil — see
 * https://react.dev/learn/you-might-not-need-an-effect.
 *
 * Must be called unconditionally, before any early return in the caller, so
 * this comparison runs on every render (store-driven transitions — manual
 * lock or the store's own expiry timer — reach it via a normal re-render;
 * a remounted caller simply starts its own local `prevUnlockedUntil` fresh).
 */
export function useEnvUnlockRemask(
  unlockedUntil: number | null,
  setEntries: Dispatch<SetStateAction<EnvEntryRow[]>>,
  entries: readonly EnvEntryRow[],
) {
  const [prevUnlockedUntil, setPrevUnlockedUntil] = useState(unlockedUntil)
  const [everMasked, setEverMasked] = useState<ReadonlySet<number>>(NO_ROWS)

  if (entries.some((e) => e.sensitive && !everMasked.has(e._rowId))) {
    // Only the rows still in the editor are kept.
    const ids = new Set(entries.map((e) => e._rowId))
    setEverMasked(
      new Set([...[...everMasked].filter((id) => ids.has(id)), ...entries.filter((e) => e.sensitive).map((e) => e._rowId)]),
    )
  }

  if (unlockedUntil !== prevUnlockedUntil) {
    setPrevUnlockedUntil(unlockedUntil)
    if (unlockedUntil === null) {
      setEntries((prev) => {
        let changed = false
        const next = prev.map((e) => {
          if (!e.sensitive && (isSensitiveKey(e.key) || everMasked.has(e._rowId))) {
            changed = true
            return { ...e, sensitive: true }
          }
          return e
        })
        return changed ? next : prev
      })
    }
  }
}
