import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  StackCardSkeleton,
  ContainerTableSkeleton,
  EditorSkeleton,
  GitHistorySkeleton,
  MetricsSkeleton,
  LoadingSpinner,
} from '../LoadingSkeleton'

describe('LoadingSkeleton', () => {
  it('StackCardSkeleton renders skeleton structure', () => {
    const { container } = render(<StackCardSkeleton />)
    const pulses = container.querySelectorAll('.animate-pulse')
    expect(pulses.length).toBeGreaterThan(0)
  })

  it('ContainerTableSkeleton renders header and rows', () => {
    render(<ContainerTableSkeleton />)
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Status')).toBeInTheDocument()
    expect(screen.getByText('Ports')).toBeInTheDocument()
  })

  it('EditorSkeleton renders code lines', () => {
    const { container } = render(<EditorSkeleton />)
    const pulses = container.querySelectorAll('.animate-pulse')
    expect(pulses.length).toBeGreaterThan(0)
  })

  it('GitHistorySkeleton renders rows', () => {
    const { container } = render(<GitHistorySkeleton />)
    const pulses = container.querySelectorAll('.animate-pulse')
    expect(pulses.length).toBeGreaterThan(0)
  })

  it('MetricsSkeleton renders 4 metric cards', () => {
    const { container } = render(<MetricsSkeleton />)
    const cards = container.querySelectorAll('.rounded-lg.border.bg-card')
    expect(cards.length).toBe(4)
  })

  it('LoadingSpinner renders with default size', () => {
    const { container } = render(<LoadingSpinner />)
    const svg = container.querySelector('svg')
    expect(svg).toBeInTheDocument()
    expect(svg?.getAttribute('class')).toBeTruthy()
  })

  it('LoadingSpinner renders with small size', () => {
    const { container } = render(<LoadingSpinner size="small" />)
    const svg = container.querySelector('svg')
    expect(svg).toBeInTheDocument()
  })

  it('LoadingSpinner renders with large size', () => {
    const { container } = render(<LoadingSpinner size="large" />)
    const svg = container.querySelector('svg')
    expect(svg).toBeInTheDocument()
  })
})
