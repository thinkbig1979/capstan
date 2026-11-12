interface Row {
  key: string
  value: string
}

/**
 * Puts the real value back into rows that only hold the redacted blank.
 *
 * A locked payload blanks every secret-looking value. An editor that kept its
 * edits across the lock (agent-os-vqf5) can therefore hold a row whose value is
 * that blank, not something the user typed, and saving it after the unlock would
 * write the blank over the secret. A row counts as the blank when its key was
 * redacted in the locked payload, its value is empty, and the baseline the
 * editor loaded also had it empty: a secret the user cleared on purpose differs
 * from a baseline that held the real value, so it is kept as typed.
 *
 * `redacted` names the keys the locked payload blanked. Duplicate keys match by
 * occurrence order. Rows are returned as is when nothing needs filling, so the
 * caller can compare by reference.
 */
export function fillRedactedBlanks<T extends Row>(
  rows: T[],
  baseline: Row[],
  redacted: ReadonlySet<string>,
  next: Row[],
): T[] {
  const seen = new Map<string, number>()
  let changed = false
  const filled = rows.map((row) => {
    if (!redacted.has(row.key)) return row
    const occurrence = seen.get(row.key) ?? 0
    seen.set(row.key, occurrence + 1)
    if (row.value !== '') return row
    const was = baseline.filter((b) => b.key === row.key)[occurrence]
    const now = next.filter((n) => n.key === row.key)[occurrence]
    if (was === undefined || was.value !== '' || now === undefined || now.value === '') return row
    changed = true
    return { ...row, value: now.value }
  })
  return changed ? filled : rows
}
