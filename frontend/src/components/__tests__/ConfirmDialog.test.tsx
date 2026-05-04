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
})
