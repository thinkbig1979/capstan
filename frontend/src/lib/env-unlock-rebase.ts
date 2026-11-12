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
 * Rows are returned as is when nothing needs filling, so the caller can compare
 * by reference.
 */
export function refillRedactedRows<T extends Identified>(
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
