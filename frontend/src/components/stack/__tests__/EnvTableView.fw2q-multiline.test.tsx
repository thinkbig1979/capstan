/**
 * agent-os-fw2q: the backend sends a multi-line quoted value as ONE read-only
 * row (a comment row carrying its key, whose value holds every line). The
 * table marks it as multi-line and points to the Raw view, where it is edited.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import { useState } from 'react'
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

/**
 * S7: in an UNLOCKED session the backend sends the secret body, so a sensitive
 * multi-line row must be masked like any other sensitive value until the eye
 * toggle reveals it, on desktop and mobile.
 */
const PEM_BODY = 'TLS_KEY="-----BEGIN PRIVATE KEY-----\ns7secretbody\n-----END PRIVATE KEY-----"'

function ToggleHarness({ initial }: { initial: EnvEntryRow[] }) {
  const [entries, setEntries] = useState(initial)
  return (
    <EnvTableView
      visible
      entries={entries}
      onEntryChange={vi.fn()}
      onDeleteEntry={vi.fn()}
      onAddEntry={vi.fn()}
      onToggleVisibility={(i) =>
        setEntries((prev) => prev.map((e, j) => (j === i ? { ...e, sensitive: !e.sensitive } : e)))
      }
      onSaveTable={vi.fn()}
      saving={false}
      hasUnsavedChanges={false}
      locked={false}
      saveRefused={false}
    />
  )
}

describe('EnvTableView sensitive multi-line rows (fw2q S7)', () => {
  it('fw2q S7: masks a sensitive multi-line value until the eye toggle reveals it, desktop and mobile', () => {
    render(
      <ToggleHarness
        initial={[{ _rowId: 1, key: 'TLS_KEY', value: PEM_BODY, sensitive: true, comment: true }]}
      />,
    )
    expect(screen.queryByText(/s7secretbody/)).toBeNull()
    // Still marked multi-line in both layouts while masked.
    expect(screen.getAllByText(/multi-line value, edit in raw/i)).toHaveLength(2)
    const toggles = screen.getAllByRole('button', { name: /toggle visibility for entry 1/i })
    expect(toggles).toHaveLength(2)
    const desktopRow = toggles[0].closest('tr') as HTMLElement
    expect(desktopRow).not.toBeNull()
    fireEvent.click(toggles[0])
    // Revealed in both layouts (one shared row state).
    expect(screen.getAllByText(/s7secretbody/)).toHaveLength(2)
    // And the toggle hides it again.
    fireEvent.click(screen.getAllByRole('button', { name: /toggle visibility for entry 1/i })[1])
    expect(screen.queryByText(/s7secretbody/)).toBeNull()
  })

  it('fw2q S7: a non-sensitive multi-line value stays visible without a toggle press', () => {
    renderTable([{ _rowId: 1, key: 'NOTE', value: 'NOTE="l1\nl2"', sensitive: false, comment: true }])
    expect(screen.getAllByText(/l1\s*l2/)).toHaveLength(2)
  })
})
