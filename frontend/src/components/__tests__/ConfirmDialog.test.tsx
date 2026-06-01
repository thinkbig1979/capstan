import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ConfirmDialog } from '../ConfirmDialog'

describe('ConfirmDialog', () => {
  it('renders nothing when open is false', () => {
    const { container } = render(
      <ConfirmDialog open={false} onOpenChange={() => {}} title="Test" description="Desc" onConfirm={() => {}} />
    )
    expect(container.innerHTML).toBe('')
  })

  it('renders title and description when open', () => {
    render(
      <ConfirmDialog open={true} onOpenChange={() => {}} title="Delete item?" description="This cannot be undone" onConfirm={() => {}} />
    )
    expect(screen.getByText('Delete item?')).toBeInTheDocument()
    expect(screen.getByText('This cannot be undone')).toBeInTheDocument()
  })

  it('calls onConfirm and closes when confirm clicked', () => {
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    render(
      <ConfirmDialog open={true} onOpenChange={onOpenChange} title="Test" description="Desc" onConfirm={onConfirm} />
    )
    fireEvent.click(screen.getByText('Confirm'))
    expect(onConfirm).toHaveBeenCalledOnce()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('calls onOpenChange(false) when cancel clicked', () => {
    const onOpenChange = vi.fn()
    render(
      <ConfirmDialog open={true} onOpenChange={onOpenChange} title="Test" description="Desc" onConfirm={() => {}} />
    )
    fireEvent.click(screen.getByText('Cancel'))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('calls onOpenChange(false) when overlay clicked', () => {
    const onOpenChange = vi.fn()
    render(
      <ConfirmDialog open={true} onOpenChange={onOpenChange} title="Test" description="Desc" onConfirm={() => {}} />
    )
    const overlay = document.querySelector('.fixed.inset-0.bg-black\\/80')
    fireEvent.click(overlay!)
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('uses destructive variant when isDangerous is true', () => {
    render(
      <ConfirmDialog open={true} onOpenChange={() => {}} title="Test" description="Desc" onConfirm={() => {}} isDangerous={true} />
    )
    const confirmBtn = screen.getByText('Confirm')
    expect(confirmBtn.closest('button')?.className).toContain('destructive')
  })

  it('uses custom confirm text', () => {
    render(
      <ConfirmDialog open={true} onOpenChange={() => {}} title="Test" description="Desc" confirmText="Delete" onConfirm={() => {}} />
    )
    expect(screen.getByText('Delete')).toBeInTheDocument()
  })

  it('keeps confirm disabled until the required text is typed (X-1)', () => {
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog
        open={true}
        onOpenChange={() => {}}
        title="Delete Stack?"
        description="Irreversible"
        confirmText="Delete"
        onConfirm={onConfirm}
        isDangerous
        requireConfirmationText="my-stack"
      />
    )
    const confirmBtn = screen.getByText('Delete').closest('button')!
    expect(confirmBtn).toBeDisabled()

    // Wrong text keeps it disabled
    const input = screen.getByLabelText('Type my-stack to confirm')
    fireEvent.change(input, { target: { value: 'wrong' } })
    expect(confirmBtn).toBeDisabled()
    fireEvent.click(confirmBtn)
    expect(onConfirm).not.toHaveBeenCalled()

    // Exact match enables it
    fireEvent.change(input, { target: { value: 'my-stack' } })
    expect(confirmBtn).toBeEnabled()
    fireEvent.click(confirmBtn)
    expect(onConfirm).toHaveBeenCalledOnce()
  })
})
