import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TablePagination } from '../TablePagination'

describe('TablePagination', () => {
  it('renders nothing for a single page', () => {
    const { container } = render(
      <TablePagination page={1} totalPages={1} pageSize={50} total={16} onPageChange={() => {}} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the range summary and disables Previous on page 1', () => {
    render(
      <TablePagination page={1} totalPages={2} pageSize={50} total={75} onPageChange={() => {}} label="entries" />,
    )
    expect(screen.getByText('Showing 1-50 of 75 entries')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Previous/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Next/ })).toBeEnabled()
  })

  it('emits the next page on Next click', () => {
    const onPageChange = vi.fn()
    render(
      <TablePagination page={1} totalPages={2} pageSize={50} total={75} onPageChange={onPageChange} />,
    )
    fireEvent.click(screen.getByRole('button', { name: /Next/ }))
    expect(onPageChange).toHaveBeenCalledWith(2)
  })
})
