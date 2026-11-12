interface Row {
  key: string
  value: string
  /** Client-side row identity, stable while the row's key and value are edited. */
  _rowId: number
}

/**
 * Puts the real value back into rows that only hold the redacted blank.
 *
 * A locked payload blanks every secret-looking value. An editor that kept its
 * edits across the lock (agent-os-vqf5) can therefore hold a row whose value is
 * that blank, not something the user typed, and saving it after the unlock would
 * write the blank over the secret. Rows are matched by `_rowId`, not by key: the
 * key input stays editable while locked, so a renamed row must still take its
 * own secret (it follows the row), a deleted row takes nothing, and a row added
 * or renamed onto the same key takes nothing from the original.
 *
 * A kept row counts as the blank when its baseline row (same `_rowId`) had a
 * redacted key and an empty value, and the kept value is still empty. A secret
 * the user cleared on purpose differs from a baseline that held the real value,
 * so it is kept as typed. The real value is the n-th `next` row with the
 * baseline row's key, n being that baseline row's position among baseline rows
 * with the same key.
 *
 * Rows are returned as is when nothing needs filling, so the caller can compare
 * by reference.
 */
export function fillRedactedBlanks<T extends Row>(
  rows: T[],
  baseline: Row[],
  redacted: ReadonlySet<string>,
  next: Array<{ key: string; value: string }>,
): T[] {
  const baselineById = new Map<number, { row: Row; occurrence: number }>()
  const seen = new Map<string, number>()
  for (const row of baseline) {
    const occurrence = seen.get(row.key) ?? 0
    seen.set(row.key, occurrence + 1)
    baselineById.set(row._rowId, { row, occurrence })
  }
  let changed = false
  const filled = rows.map((row) => {
    const was = baselineById.get(row._rowId)
    if (!was || was.row.value !== '' || row.value !== '' || !redacted.has(was.row.key)) return row
    const now = next.filter((n) => n.key === was.row.key)[was.occurrence]
    if (now === undefined || now.value === '') return row
    changed = true
    return { ...row, value: now.value }
  })
  return changed ? filled : rows
}
