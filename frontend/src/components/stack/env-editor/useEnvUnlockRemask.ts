import { useState, type Dispatch, type SetStateAction } from 'react'
import { isSensitiveKey } from './sensitiveKey'
import type { EnvEntryRow } from './types'

const NO_ROWS: ReadonlySet<number> = new Set()

type HistorySnapshot = { entries: EnvEntryRow[]; raw: string }

/**
 * When the unlock session ends (manual lock or auto-expiry), re-mask every
 * entry the user had revealed during the session: one whose key looks secret,
 * and one that was masked since it entered the editor, which covers a value
 * the server flagged under a harmless key (agent-os-ruuw). A reveal is stored
 * as `sensitive: false` on the row, so the rows ever masked are remembered by
 * `_rowId`. The undo history is re-masked the same way (agent-os-y4gh).
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
  history: {
    snapshots: readonly HistorySnapshot[]
    rebase: (fn: (snapshot: HistorySnapshot) => HistorySnapshot) => void
  },
) {
  const [prevUnlockedUntil, setPrevUnlockedUntil] = useState(unlockedUntil)
  const [everMasked, setEverMasked] = useState<ReadonlySet<number>>(NO_ROWS)

  if (entries.some((e) => e.sensitive && !everMasked.has(e._rowId))) {
    // Never pruned: a deleted row can come back through Undo after the history
    // has dropped every snapshot that showed it masked (agent-os-y4gh). It grows
    // only with the rows this editor has seen.
    setEverMasked(new Set([...everMasked, ...entries.filter((e) => e.sensitive).map((e) => e._rowId)]))
  }

  if (unlockedUntil !== prevUnlockedUntil) {
    setPrevUnlockedUntil(unlockedUntil)
    if (unlockedUntil === null) {
      // A row masked in any snapshot counts too, so a snapshot's own record
      // holds even if the render-time check above has not seen that row.
      const masked = new Set(everMasked)
      for (const snapshot of history.snapshots) {
        for (const e of snapshot.entries) if (e.sensitive) masked.add(e._rowId)
      }
      const remask = (rows: EnvEntryRow[]) => {
        let changed = false
        const next = rows.map((e) => {
          // Every comment row too: the server flags a multi-line secret's body
          // rows and a commented-out secret only in the payload it redacts, so
          // rows kept from an unlocked payload carry no flag (agent-os-y6e8).
          if (!e.sensitive && (e.comment === true || isSensitiveKey(e.key) || masked.has(e._rowId))) {
            changed = true
            return { ...e, sensitive: true }
          }
          return e
        })
        return changed ? next : rows
      }
      setEntries(remask)
      // A reveal is stored in the undo history like any edit, so Undo or Redo
      // would restore it after the lock (agent-os-y4gh): re-mask the snapshots.
      history.rebase((snapshot) => {
        const next = remask(snapshot.entries)
        return next === snapshot.entries ? snapshot : { ...snapshot, entries: next }
      })
    }
  }

  /**
   * Records rows as masked whatever they show now. A refetch gives every row a
   * new id and carries a reveal by key, so a revealed secret arrives under an id
   * that was never masked on screen (agent-os-oeqf).
   */
  const rememberMasked = (ids: readonly number[]) => {
    if (ids.length > 0) setEverMasked((prev) => new Set([...prev, ...ids]))
  }

  /** The row was masked at some point since it entered the editor. */
  const wasMasked = (id: number) => everMasked.has(id)

  return { rememberMasked, wasMasked }
}
