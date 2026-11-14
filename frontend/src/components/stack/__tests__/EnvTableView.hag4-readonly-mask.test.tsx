/**
 * agent-os-hag4: the backend marks a sensitive one-line read-only row
 * (`DB_PASSWORD: s3cret`, which compose reads as a variable) as sensitive. An
 * unlocked session receives its value, so the table masks it until the eye
 * toggle reveals it, as it does a multi-line one (fw2q S7). Every keyed
 * read-only row gets the toggle; a free-text comment has no key and stays
 * plain.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { useState } from 'react'
import { EnvTableView } from '../env-editor/EnvTableView'
import type { EnvEntryRow } from '../env-editor/types'

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

const SECRET_ROW: EnvEntryRow = {
  _rowId: 1,
  key: 'DB_PASSWORD',
  value: 'DB_PASSWORD: s3cret',
  sensitive: true,
  comment: true,
}

const toggles = () => screen.getAllByRole('button', { name: /toggle visibility for entry 1/i })

describe('EnvTableView sensitive one-line read-only rows (hag4)', () => {
  it('hag4: hides the value until the desktop toggle, and the mobile toggle hides it again', () => {
    render(<ToggleHarness initial={[SECRET_ROW]} />)
    expect(screen.queryAllByText(/s3cret/)).toHaveLength(0)
    expect(toggles()).toHaveLength(2) // desktop row and mobile card
    expect(toggles()[0].closest('tr')).not.toBeNull()

    fireEvent.click(toggles()[0])
    expect(screen.getAllByText(/s3cret/)).toHaveLength(2)

    fireEvent.click(toggles()[1])
    expect(screen.queryAllByText(/s3cret/)).toHaveLength(0)
  })

  it('hag4: the mobile toggle reveals it, and the desktop toggle hides it again', () => {
    render(<ToggleHarness initial={[SECRET_ROW]} />)
    fireEvent.click(toggles()[1])
    expect(screen.getAllByText(/s3cret/)).toHaveLength(2)

    fireEvent.click(toggles()[0])
    expect(screen.queryAllByText(/s3cret/)).toHaveLength(0)
  })

  it('hag4: a non-sensitive read-only row stays plain', () => {
    render(
      <ToggleHarness initial={[{ _rowId: 1, key: 'BAR', value: 'BAR: plainvalue', sensitive: false, comment: true }]} />,
    )
    expect(screen.getAllByText('BAR: plainvalue')).toHaveLength(2)
  })

  it('hag4: a free-text comment stays plain, with no toggle', () => {
    render(
      <ToggleHarness initial={[{ _rowId: 1, key: '', value: '# just a note', sensitive: false, comment: true }]} />,
    )
    expect(screen.getAllByText('# just a note')).toHaveLength(2)
    expect(screen.queryByRole('button', { name: /toggle visibility/i })).toBeNull()
  })
})
