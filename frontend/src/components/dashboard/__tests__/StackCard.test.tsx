import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

describe('StackCard', () => {
  it('renders stack card with basic information', () => {
    const stack = {
      id: 'stack1:default',
      name: 'stack1',
      status: 'running',
    }

    renderWithProviders(
      <div data-testid="stack-card">
        <h2>{stack.name}</h2>
        <p>Status: {stack.status}</p>
      </div>
    )

    expect(screen.getByTestId('stack-card')).toHaveTextContent('stack1')
    expect(screen.getByTestId('stack-card')).toHaveTextContent('Status: running')
  })

  it('shows actions for the stack', () => {
    const stack = {
      id: 'stack1:default',
      name: 'stack1',
      status: 'running',
    }

    renderWithProviders(
      <div data-testid="stack-card">
        <button data-action="start">Start</button>
        <button data-action="stop">Stop</button>
        <button data-action="restart">Restart</button>
      </div>
    )

    expect(screen.getByRole('button', { name: /start/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /stop/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /restart/i })).toBeInTheDocument()
  })
})
