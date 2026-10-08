import { expect } from 'vitest'

/**
 * A spanning row (empty state, expanded detail) must cover exactly as many
 * columns as its own table's header row has, so adding a column cannot leave it
 * under-spanning the body (agent-os-o9fx). Reads the header count off the same
 * <table> the cell sits in, because a file can hold more than one table.
 */
export function expectSpansItsHeader(node: HTMLElement | null) {
  const cell = node?.closest('td') ?? null
  expect(cell).not.toBeNull()
  const table = cell!.closest('table')
  expect(table).not.toBeNull()
  const headerCount = table!.querySelectorAll('thead th').length
  expect(headerCount).toBeGreaterThan(0)
  expect(cell!.getAttribute('colspan')).toBe(String(headerCount))
}
