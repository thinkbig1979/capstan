import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { GlobalEnvTable } from '../global-env/GlobalEnvTable'
import { GlobalEnvCards } from '../global-env/GlobalEnvCards'
import type { IndexedEnvVar } from '../global-env/types'

// agent-os-ruuw: the page masked by key name only, so a secret-shaped value
// under a harmless key was shown in full to an unlocked session. The server
// now flags it, and the page masks on the flag or the key name.

const PEM = '"-----BEGIN PRIVATE KEY-----\nruuwpemsecret\n-----END PRIVATE KEY-----"'
const ROWS: IndexedEnvVar[] = [
  { key: 'NOTES', value: 'https://u:ruuwurlsecret@example.com', sensitive: true, _originalIndex: 0 },
  { key: 'TLS', value: PEM, sensitive: true, _originalIndex: 1 },
  { key: 'TZ', value: 'UTC', _originalIndex: 2 },
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

describe.each(views)('GlobalEnv %s: a value the server flags sensitive (agent-os-ruuw)', (_name, View) => {
  it('masks a one-line value under a harmless key until revealed', () => {
    render(<Harness View={View} />)
    const input = screen.getByLabelText('Global env value 1')
    expect(input).toHaveAttribute('type', 'password')

    fireEvent.click(screen.getByLabelText('Show value 1'))
    expect(screen.getByLabelText('Global env value 1')).toHaveAttribute('type', 'text')
  })

  it('masks a multi-line value under a harmless key until revealed', () => {
    render(<Harness View={View} />)
    expect(screen.queryByText(/ruuwpemsecret/)).toBeNull()
    expect(screen.getByText('••••••')).toBeInTheDocument()

    fireEvent.click(screen.getByLabelText('Show value 2'))
    expect(screen.getByText(/ruuwpemsecret/)).toBeInTheDocument()
  })

  it('leaves an unflagged value under a harmless key plain', () => {
    render(<Harness View={View} />)
    expect(screen.getByLabelText('Global env value 3')).toHaveAttribute('type', 'text')
    expect(screen.queryByLabelText('Show value 3')).toBeNull()
  })
})
