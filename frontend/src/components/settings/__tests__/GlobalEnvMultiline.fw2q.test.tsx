/**
 * agent-os-fw2q / agent-os-rd1l (C2): a global variable whose value spans
 * several lines cannot be edited in a one-line input, and the server refuses a
 * change to it. The table and the mobile cards show it read-only, key too,
 * with a marker; the global page has no Raw view, so the marker does not point
 * to one.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GlobalEnvTable } from '../global-env/GlobalEnvTable'
import { GlobalEnvCards } from '../global-env/GlobalEnvCards'
import type { IndexedEnvVar } from '../global-env/types'

const ROWS: IndexedEnvVar[] = [
  { key: 'TZ', value: 'Europe/Amsterdam', _originalIndex: 0 },
  { key: 'BANNER', value: 'line one\nline two', _originalIndex: 1 },
]

function renderBoth(rows: IndexedEnvVar[], visible: Record<number, boolean> = {}) {
  const props = {
    hasVars: true,
    filtered: rows,
    query: '',
    visible,
    onChange: vi.fn(),
    onToggleVisible: vi.fn(),
    onDelete: vi.fn(),
  }
  render(
    <>
      <GlobalEnvTable {...props} />
      <GlobalEnvCards {...props} />
    </>,
  )
  return props
}

describe('Global env multi-line values (fw2q C2)', () => {
  it('shows a multi-line value read-only with a marker, key read-only, desktop and mobile', () => {
    renderBoth(ROWS)
    expect(screen.getAllByText(/multi-line value, read-only here/i)).toHaveLength(2)
    expect(screen.queryByText(/edit in raw/i)).toBeNull()
    // No one-line value input for the multi-line row in either layout.
    expect(screen.queryAllByLabelText('Global env value 2')).toHaveLength(0)
    const keys = screen.getAllByLabelText('Global env key 2')
    expect(keys).toHaveLength(2)
    for (const k of keys) expect(k).toBeDisabled()
    expect(screen.getAllByText(/line one\s+line two/)).toHaveLength(2)
    // The one-line row stays editable.
    for (const k of screen.getAllByLabelText('Global env key 1')) expect(k).toBeEnabled()
    expect(screen.getAllByLabelText('Global env value 1')).toHaveLength(2)
  })

  it('masks a multi-line value under a sensitive key until revealed', () => {
    const rows: IndexedEnvVar[] = [{ key: 'TLS_PRIVATE_KEY', value: 'c2secret\nmore', _originalIndex: 0 }]
    renderBoth(rows)
    expect(screen.queryByText(/c2secret/)).toBeNull()
    expect(screen.getAllByLabelText('Show value 1')).toHaveLength(2)
  })

  it('shows a revealed sensitive multi-line value', () => {
    const rows: IndexedEnvVar[] = [{ key: 'TLS_PRIVATE_KEY', value: 'c2secret\nmore', _originalIndex: 0 }]
    renderBoth(rows, { 0: true })
    expect(screen.getAllByText(/c2secret/)).toHaveLength(2)
  })
})
