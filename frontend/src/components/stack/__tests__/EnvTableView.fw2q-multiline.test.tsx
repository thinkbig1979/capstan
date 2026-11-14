/**
 * agent-os-fw2q: the backend sends a multi-line quoted value as ONE read-only
 * row (a comment row carrying its key, whose value holds every line). The
 * table marks it as multi-line and points to the Raw view, where it is edited.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { EnvTableView } from '../env-editor/EnvTableView'
import type { EnvEntryRow } from '../env-editor/types'

const CERT = 'CERT_KEY="-----BEGIN\nabc\n-----END"'

function renderTable(entries: EnvEntryRow[]) {
  return render(
    <EnvTableView
      visible
      entries={entries}
      onEntryChange={vi.fn()}
      onDeleteEntry={vi.fn()}
      onAddEntry={vi.fn()}
      onToggleVisibility={vi.fn()}
      onSaveTable={vi.fn()}
      saving={false}
      hasUnsavedChanges={false}
      locked={false}
      saveRefused={false}
    />,
  )
}

describe('EnvTableView multi-line rows (fw2q)', () => {
  it('fw2q: marks a multi-line read-only row as edited in Raw, with its key read-only', () => {
    renderTable([
      { _rowId: 1, key: 'A', value: '1', sensitive: false, comment: false },
      { _rowId: 2, key: 'CERT_KEY', value: CERT, sensitive: true, comment: true },
    ])
    // Desktop table and mobile cards both render; each marks the row once.
    const markers = screen.getAllByText(/multi-line value, edit in raw/i)
    expect(markers).toHaveLength(2)
    const row = markers[0].closest('tr') as HTMLElement
    expect(within(row).getByDisplayValue('CERT_KEY')).toBeDisabled()
    // No editable value input on the row: the value is not a one-line field.
    expect(within(row).queryByLabelText(/Environment variable value/)).toBeNull()
  })

  it('fw2q: a one-line read-only row carries no multi-line marker', () => {
    renderTable([{ _rowId: 1, key: 'FOO', value: 'FOO', sensitive: false, comment: true }])
    expect(screen.queryByText(/multi-line value, edit in raw/i)).toBeNull()
  })
})
