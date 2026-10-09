import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EmptyState } from '../EmptyState'

describe('EmptyState', () => {
  it('renders title', () => {
    render(<EmptyState title="Nothing here" />)
    expect(screen.getByText('Nothing here')).toBeInTheDocument()
  })

  it('renders description when provided', () => {
    render(<EmptyState title="Empty" description="No items found" />)
    expect(screen.getByText('No items found')).toBeInTheDocument()
  })

  it('does not render description when omitted', () => {
    const { container } = render(<EmptyState title="Empty" />)
    expect(container.querySelector('.text-muted-foreground.mb-4')).not.toBeInTheDocument()
  })

  it('renders custom icon when provided', () => {
    render(<EmptyState title="Empty" icon={<span data-testid="custom-icon">X</span>} />)
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument()
  })

  it('renders default icon when no icon provided', () => {
    render(<EmptyState title="Empty" />)
    expect(document.querySelector('svg')).toBeInTheDocument()
  })

  it('renders action when provided', () => {
    render(<EmptyState title="Empty" action={<button>Action</button>} />)
    expect(screen.getByRole('button', { name: 'Action' })).toBeInTheDocument()
  })
})
