import { describe, it, expect } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { StatusBadge } from '../StatusBadge'

describe('StatusBadge', () => {
  it('renders running status', () => {
    renderWithProviders(<StatusBadge status="running" pulse={false} />)
    expect(screen.getByText('Running')).toBeInTheDocument()
  })

  it('renders stopped status', () => {
    renderWithProviders(<StatusBadge status="stopped" pulse={false} />)
    expect(screen.getByText('Stopped')).toBeInTheDocument()
  })

  it('renders partial status', () => {
    renderWithProviders(<StatusBadge status="partial" pulse={false} />)
    expect(screen.getByText('Partial')).toBeInTheDocument()
  })

  it('renders unknown status', () => {
    renderWithProviders(<StatusBadge status="unknown" pulse={false} />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('applies pulse animation when running and pulse is true', () => {
    renderWithProviders(<StatusBadge status="running" pulse={true} />)
    const badge = screen.getByText('Running')
    expect(badge.closest('[class]')?.className).toContain('animate-pulse')
  })

  it('does not pulse when pulse is false', () => {
    renderWithProviders(<StatusBadge status="running" pulse={false} />)
    const badge = screen.getByText('Running')
    expect(badge.closest('[class]')?.className).not.toContain('animate-pulse')
  })

  it('does not pulse for non-running statuses even with pulse=true', () => {
    renderWithProviders(<StatusBadge status="stopped" pulse={true} />)
    const badge = screen.getByText('Stopped')
    expect(badge.closest('[class]')?.className).not.toContain('animate-pulse')
  })

  it('applies green color classes for running', () => {
    renderWithProviders(<StatusBadge status="running" pulse={false} />)
    const badge = screen.getByText('Running')
    const cls = badge.closest('[class]')?.className || ''
    expect(cls).toContain('green')
  })

  it('applies red color classes for stopped', () => {
    renderWithProviders(<StatusBadge status="stopped" pulse={false} />)
    const badge = screen.getByText('Stopped')
    const cls = badge.closest('[class]')?.className || ''
    expect(cls).toContain('red')
  })

  it('applies yellow color classes for partial', () => {
    renderWithProviders(<StatusBadge status="partial" pulse={false} />)
    const badge = screen.getByText('Partial')
    const cls = badge.closest('[class]')?.className || ''
    expect(cls).toContain('yellow')
  })
})
