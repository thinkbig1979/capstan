import { describe, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GlobalEnvTable } from '../global-env/GlobalEnvTable'
import { expectSpansItsHeader } from '@/test/tableSpan'

// agent-os-o9fx: both placeholder rows are spanning rows of the one table.
function renderTable(props: { hasVars: boolean; query: string }) {
  return render(
    <GlobalEnvTable
      {...props}
      filtered={[]}
      visible={{}}
      onChange={vi.fn()}
      onToggleVisible={vi.fn()}
      onDelete={vi.fn()}
    />,
  )
}

describe('GlobalEnvTable — spanning rows', () => {
  it('spans the empty-list row across every column', () => {
    renderTable({ hasVars: false, query: '' })

    expectSpansItsHeader(screen.getByText(/No global variables yet/))
  })

  it('spans the no-match row across every column', () => {
    renderTable({ hasVars: true, query: 'zzz' })

    expectSpansItsHeader(screen.getByText('No variables match "zzz".'))
  })
})
