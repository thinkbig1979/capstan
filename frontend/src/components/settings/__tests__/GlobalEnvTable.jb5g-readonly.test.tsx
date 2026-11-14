import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { GlobalEnvTable } from '../global-env/GlobalEnvTable'
import { GlobalEnvCards } from '../global-env/GlobalEnvCards'
import type { IndexedEnvVar } from '../global-env/types'

// agent-os-jb5g: a bare KEY and a "KEY: value" line were shown as editable
// rows, and an edit rewrote them as KEY=x. The server marks them readOnly; the
// page shows them read-only with a marker, as it does a multi-line value.

const ROWS: IndexedEnvVar[] = [
  { key: 'FOO', value: '', readOnly: true, _originalIndex: 0 },
  { key: 'BAR', value: '1', readOnly: true, _originalIndex: 1 },
  { key: 'API_TOKEN', value: 'jb5gsecret', readOnly: true, sensitive: true, _originalIndex: 2 },
  { key: 'A', value: '1', _originalIndex: 3 },
]

const views = [
  ['desktop table', GlobalEnvTable],
  ['mobile cards', GlobalEnvCards],
] as const

function Harness({ View }: { View: (typeof views)[number][1] }) {
  const [visible, setVisible] = useState<Record<number, boolean>>({})
  return (
    <View
      hasVars
      filtered={ROWS}
      query=""
      visible={visible}
      onChange={vi.fn()}
      onToggleVisible={(i) => setVisible((prev) => ({ ...prev, [i]: !prev[i] }))}
      onDelete={vi.fn()}
    />
  )
}

describe.each(views)('GlobalEnv %s: read-only lines (agent-os-jb5g)', (_name, View) => {
  it('shows a bare KEY and a KEY: value row read-only, with a marker', () => {
    render(<Harness View={View} />)
    for (const n of [1, 2, 3]) {
      expect(screen.queryByLabelText(`Global env value ${n}`)).toBeNull()
      expect(screen.getByLabelText(`Global env key ${n}`)).toBeDisabled()
    }
    expect(screen.getAllByText(/read-only here, edit global\.env directly/i)).toHaveLength(3)
    expect(screen.getByText('1', { selector: 'span' })).toBeInTheDocument()
  })

  it('masks a sensitive read-only row until revealed', () => {
    render(<Harness View={View} />)
    expect(screen.queryByText('jb5gsecret')).toBeNull()
    fireEvent.click(screen.getByLabelText('Show value 3'))
    expect(screen.getByText('jb5gsecret')).toBeInTheDocument()
  })

  it('keeps a KEY=value row editable', () => {
    render(<Harness View={View} />)
    expect(screen.getByLabelText('Global env value 4')).not.toBeDisabled()
    expect(screen.getByLabelText('Global env key 4')).not.toBeDisabled()
  })
})
