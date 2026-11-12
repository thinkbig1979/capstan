interface Lined {
  key: string
  /** File line, where the payload carries one (stack env entries do). */
  line?: number
  comment?: boolean
}

interface Identified {
  /** Client-side row identity, stable while the row's key and value are edited. */
  _rowId: number
}

interface Shown {
  key: string
  value: string
}

/**
 * A row the locked view showed with no key and no value (a multi-line secret's
 * body row, or any row of a global env payload whose keys were all blanked):
 * the user could not tell which variable it was (agent-os-yt2f review S1, S2).
 */
const isHidden = (row: Shown) => row.key === '' && row.value === ''

/** "Nothing was redacted": the baseline already holds real values. */
export const NO_REDACTED_ROWS: ReadonlySet<number> = new Set()

/**
 * Lines up the baseline an editor kept across an unlock (agent-os-vqf5) with the
 * unlocked payload, so each redacted row can take its real content.
 *
 * A locked payload hides more than a value (agent-os-yt2f): a commented secret
 * is cut to "#KEY=", the rows of a multi-line secret lose key and value, a URL
 * credential is starred, a key can be blanked. None of these can be matched by
 * key, so rows are matched by POSITION, and only when the two payloads plainly
 * describe the same file: the same number of rows and, at every position, the
 * same line, the same comment flag, and the same key wherever the locked key is
 * not blank (a redacted body row's key is blank while locked and can be its
 * text once unlocked).
 *
 * Returns the unlocked row for each redacted baseline row, by `_rowId`, or null
 * when the payloads do not line up. The caller must then write nothing it would
 * have taken from `next`. With nothing redacted there is nothing to line up.
 */
export function alignRedactedRows<B extends Lined & Identified, N extends Lined>(
  baseline: B[],
  redacted: ReadonlySet<number>,
  next: N[],
): Map<number, N> | null {
  const aligned = new Map<number, N>()
  if (redacted.size === 0) return aligned
  if (baseline.length !== next.length) return null
  for (let i = 0; i < baseline.length; i++) {
    const was = baseline[i]
    const now = next[i]
    if (was.line !== now.line || !!was.comment !== !!now.comment) return null
    if (was.key !== '' && was.key !== now.key) return null
    if (redacted.has(was._rowId)) aligned.set(was._rowId, now)
  }
  return aligned
}

/**
 * Puts the real content back into redacted rows, field by field: every listed
 * field the user left as the locked baseline had it takes the unlocked value,
 * and every field the user changed stays as typed. So an untouched row becomes
 * the real row, a renamed row keeps its new key and takes its own secret, a
 * deleted row takes nothing, and a row added while locked has no baseline row
 * and takes nothing either. Rows are followed by `_rowId`, never by key.
 *
 * A hidden row the user edited takes nothing at all: they could not see which
 * variable it was, so a typed key must not inherit its secret. holdsBlindEdit
 * then refuses the save.
 *
 * Rows are returned as is when nothing needs filling, so the caller can compare
 * by reference.
 */
export function refillRedactedRows<T extends Identified & Shown>(
  rows: T[],
  baseline: T[],
  aligned: ReadonlyMap<number, Partial<T>>,
  fields: ReadonlyArray<keyof T>,
): T[] {
  if (aligned.size === 0) return rows
  const baselineById = new Map(baseline.map((row) => [row._rowId, row]))
  let changed = false
  const filled = rows.map((row) => {
    const was = baselineById.get(row._rowId)
    const now = aligned.get(row._rowId)
    if (!was || !now) return row
    if (isHidden(was) && (row.key !== was.key || row.value !== was.value)) return row
    let out = row
    for (const field of fields) {
      if (row[field] !== was[field] || now[field] === was[field]) continue
      if (out === row) out = { ...row }
      out[field] = now[field] as T[keyof T]
    }
    if (out !== row) changed = true
    return out
  })
  return changed ? filled : rows
}

/**
 * The ids of the hidden rows the user edited blind while locked (agent-os-yt2f
 * review S1, S2): deleted, which would cut a line out of a multi-line secret,
 * or given a typed key or value. Call it at the unlock, before the refill, with
 * every row list that can come back (the rows on screen and each Undo
 * snapshot), so a blind edit the user undid and could Redo is caught too.
 */
export function blindEditedRowIds<T extends Identified & Shown>(
  rowLists: T[][],
  lockedBaseline: T[],
  aligned: ReadonlyMap<number, Shown>,
): Set<number> {
  const ids = new Set<number>()
  const listsById = rowLists.map((rows) => new Map(rows.map((row) => [row._rowId, row])))
  for (const was of lockedBaseline) {
    if (!aligned.has(was._rowId) || !isHidden(was)) continue
    const edited = listsById.some((byId) => {
      const row = byId.get(was._rowId)
      return !row || row.key !== was.key || row.value !== was.value
    })
    if (edited) ids.add(was._rowId)
  }
  return ids
}

/**
 * True while a row edited blind does not hold its real content, so saving would
 * write over content the user never saw. A row that holds exactly the unlocked
 * key and value (an Undo back to before the edit) is safe again; a row edited
 * after the unlock, when it could be seen, is never in `blindIds`.
 */
export function holdsBlindEdit<T extends Identified & Shown>(
  rows: T[],
  blindIds: ReadonlySet<number>,
  aligned: ReadonlyMap<number, Shown>,
): boolean {
  if (blindIds.size === 0) return false
  const rowsById = new Map(rows.map((row) => [row._rowId, row]))
  return [...blindIds].some((id) => {
    const row = rowsById.get(id)
    const now = aligned.get(id)
    return !row || !now || row.key !== now.key || row.value !== now.value
  })
}
