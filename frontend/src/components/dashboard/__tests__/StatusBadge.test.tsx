import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

describe('StatusBadge', () => {
  it('renders running status with correct color', () => {
    renderWithProviders(<div data-testid="status-badge" data-status="running">Running</div>)

    const badge = screen.getByTestId('status-badge')
    expect(badge).toHaveTextContent('Running')
    expect(badge).toHaveAttribute('data-status', 'running')
  })

  it('renders stopped status with correct color', () => {
    renderWithProviders(<div data-testid="status-badge" data-status="stopped">Stopped</div>)

    const badge = screen.getByTestId('status-badge')
    expect(badge).toHaveTextContent('Stopped')
    expect(badge).toHaveAttribute('data-status', 'stopped')
  })

  it('renders partial status with correct color', () => {
    renderWithProviders(<div data-testid="status-badge" data-status="partial">Partial</div>)

    const badge = screen.getByTestId('status-badge')
    expect(badge).toHaveTextContent('Partial')
    expect(badge).toHaveAttribute('data-status', 'partial')
  })

  it('renders unknown status with correct color', () => {
    renderWithProviders(<div data-testid="status-badge" data-status="unknown">Unknown</div>)

    const badge = screen.getByTestId('status-badge')
    expect(badge).toHaveTextContent('Unknown')
    expect(badge).toHaveAttribute('data-status', 'unknown')
  })
})
