/**
 * agent-os-9fsu: the server flags every line of a multi-line secret as
 * sensitive, in the unlocked payload too, and marks it `bodyLine`. A body line
 * that parses as KEY= (base64 padding) arrives as a variable named by the line
 * with the value "="; the flag alone cannot tell it from `DB_PASSWORD=x`, whose
 * key is the name and stays visible, so `bodyLine` is what hides the key. While
 * the session is locked the value of such a row cannot be typed over either
 * (agent-os-9fsu F10): on an unlocked baseline the text would replace the real
 * value at the next save.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EnvTableView } from '../env-editor/EnvTableView'
import type { EnvEntryRow } from '../env-editor/types'

const BODY = 'QkZHSlastLineXyZ'
const bodyRow = (over: Partial<EnvEntryRow> = {}): EnvEntryRow => ({
  _rowId: 2,
  key: BODY,
  value: '=',
  line: 4,
  sensitive: true,
  bodyLine: true,
  ...over,
})
const PORT: EnvEntryRow = { _rowId: 1, key: 'PORT', value: '8080', line: 1, sensitive: false }
const PASSWORD: EnvEntryRow = { _rowId: 3, key: 'DB_PASSWORD', value: 'hunter2', line: 5, sensitive: true }

function renderTable(entries: EnvEntryRow[], extra: { locked?: boolean; lockedNow?: boolean } = {}) {
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
      locked={extra.locked ?? false}
      lockedNow={extra.lockedNow}
      saveRefused={false}
    />,
  )
}
const keyInputs = () => screen.getAllByLabelText(/^Environment variable key \d+$/) as HTMLInputElement[]
const valueInput = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`) as HTMLInputElement[]

describe('EnvTableView: a flagged multi-line body row (agent-os-9fsu)', () => {
  it('shows no key for a masked body row, in the table and the card, and keeps real keys', () => {
    renderTable([PORT, bodyRow(), PASSWORD])

    const keys = keyInputs().map((el) => el.value)
    expect(keys.filter((k) => k.includes(BODY))).toEqual([])
    // Positive side: the keys of the other rows, a sensitive one included, are shown.
    expect(keys).toContain('PORT')
    expect(keys).toContain('DB_PASSWORD')
  })

  it('does not let the key be typed over while masked', () => {
    renderTable([bodyRow()])
    expect(keyInputs().map((el) => el.disabled)).toEqual([true, true])
  })

  it('search does not match the key of a masked body row, and still finds a shown row', () => {
    renderTable([PORT, bodyRow()])
    const search = screen.getByPlaceholderText('Filter env vars…')

    fireEvent.change(search, { target: { value: 'QkZHSlast' } })
    expect(screen.queryAllByLabelText(/^Environment variable key \d+$/)).toHaveLength(0)
    fireEvent.change(search, { target: { value: 'PORT' } })
    expect(screen.queryAllByLabelText(/^Environment variable key \d+$/).length).toBeGreaterThan(0)
  })

  it('shows the key once the row is revealed (sensitive false), the flag no longer hides it', () => {
    renderTable([bodyRow({ sensitive: false })])
    expect(keyInputs().map((el) => [el.value, el.disabled])).toEqual([
      [BODY, false],
      [BODY, false],
    ])
  })

  it('F10: the value of a key-hidden row is disabled while the session is locked, either way it is known', () => {
    for (const extra of [{ locked: true }, { lockedNow: true }]) {
      const { unmount } = renderTable([bodyRow()], extra)
      expect(valueInput(1).map((el) => el.disabled)).toEqual([true, true])
      unmount()
    }
  })

  it('F10: positive side, the value of a keyed sensitive row stays editable while locked', () => {
    renderTable([PASSWORD], { locked: true, lockedNow: true })
    expect(valueInput(1).map((el) => el.disabled)).toEqual([false, false])
  })

  it('F10: unlocked, a masked body row can still be revealed and edited, so the value is not disabled', () => {
    renderTable([bodyRow()])
    expect(valueInput(1).map((el) => el.disabled)).toEqual([false, false])
  })
})
