import { describe, it, expect } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { StatusBadge, type Status } from '../StatusBadge'

function getBadgeByText(text: string) {
  return screen.getByText(text).closest('[data-slot="badge"]') as HTMLElement
}

function getDot() {
  const badge = document.querySelector('[data-slot="badge"]')
  return badge?.querySelector('span:not([data-slot])') as HTMLElement | null
}

describe('StatusBadge', () => {
  describe('renders correct label for each status', () => {
    it.each([
      ['running', 'Running'],
      ['stopped', 'Stopped'],
      ['partial', 'Partial'],
      ['unknown', 'Unknown'],
    ] as [Status, string][])('renders label "%s" for status "%s"', (status, label) => {
      renderWithProviders(<StatusBadge status={status} pulse={false} />)
      expect(screen.getByText(label)).toBeInTheDocument()
    })
  })

  describe('green dot for running status', () => {
    it('renders a green dot span when status is running', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const dot = getDot()
      expect(dot).toBeInTheDocument()
      expect(dot?.className).toContain('rounded-full')
      expect(dot?.className).toContain('bg-green-500')
    })

    it('dot is a small 2x2 element', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const dot = getDot()
      expect(dot?.className).toContain('h-2')
      expect(dot?.className).toContain('w-2')
    })

    it('dot appears before the Running text', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const badge = getBadgeByText('Running')
      const dot = badge.querySelector('span:not([data-slot])')
      expect(dot).toBeInTheDocument()
      expect(dot?.className).toContain('bg-green-500')
    })

    it('does not render a dot for non-running statuses', () => {
      renderWithProviders(<StatusBadge status="stopped" pulse={false} />)
      const badge = screen.getByText('Stopped').closest('[data-slot="badge"]')
      expect(badge?.querySelector('span:not([data-slot])')).not.toBeInTheDocument()
    })
  })

  describe('pulse animation', () => {
    it('dot has animate-pulse when pulse prop is true (default)', () => {
      renderWithProviders(<StatusBadge status="running" pulse={true} />)
      const dot = getDot()
      expect(dot?.className).toContain('animate-pulse')
    })

    it('dot has animate-pulse when pulse prop is omitted (defaults to true)', () => {
      renderWithProviders(<StatusBadge status="running" />)
      const dot = getDot()
      expect(dot?.className).toContain('animate-pulse')
    })

    it('dot does NOT have animate-pulse when pulse prop is false', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const dot = getDot()
      expect(dot?.className).not.toContain('animate-pulse')
    })
  })

  describe('applies correct color classes for each status', () => {
    it('applies green color classes for running', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const cls = getBadgeByText('Running').className
      expect(cls).toContain('bg-green-500/15')
      expect(cls).toContain('text-green-700')
      expect(cls).toContain('border-green-500/25')
    })

    it('applies red color classes for stopped', () => {
      renderWithProviders(<StatusBadge status="stopped" pulse={false} />)
      const cls = getBadgeByText('Stopped').className
      expect(cls).toContain('bg-red-500/15')
      expect(cls).toContain('text-red-700')
      expect(cls).toContain('border-red-500/25')
    })

    it('applies yellow color classes for partial', () => {
      renderWithProviders(<StatusBadge status="partial" pulse={false} />)
      const cls = getBadgeByText('Partial').className
      expect(cls).toContain('bg-yellow-500/15')
      expect(cls).toContain('text-yellow-700')
      expect(cls).toContain('border-yellow-500/25')
    })

    it('applies gray color classes for unknown', () => {
      renderWithProviders(<StatusBadge status="unknown" pulse={false} />)
      const cls = getBadgeByText('Unknown').className
      expect(cls).toContain('bg-gray-500/15')
      expect(cls).toContain('text-gray-700')
      expect(cls).toContain('border-gray-500/25')
    })
  })

  describe('falls back to unknown for invalid status values', () => {
    it('renders Unknown label for an unrecognized status', () => {
      renderWithProviders(<StatusBadge status={'bogus' as Status} pulse={false} />)
      expect(screen.getByText('Unknown')).toBeInTheDocument()
    })

    it('applies gray color classes for an unrecognized status', () => {
      renderWithProviders(<StatusBadge status={'bogus' as Status} pulse={false} />)
      const cls = getBadgeByText('Unknown').className
      expect(cls).toContain('bg-gray-500/15')
      expect(cls).toContain('text-gray-700')
    })

    it('does not render a dot for an unrecognized status', () => {
      renderWithProviders(<StatusBadge status={'bogus' as Status} pulse={true} />)
      const badge = screen.getByText('Unknown').closest('[data-slot="badge"]')
      expect(badge?.querySelector('span:not([data-slot])')).not.toBeInTheDocument()
    })
  })

  describe('tooltip', () => {
    it('renders tooltip trigger wrapping the badge', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const badge = getBadgeByText('Running')
      expect(badge).toHaveAttribute('data-state', 'closed')
    })

    it('wraps the badge in a tooltip trigger', () => {
      const { container } = renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const trigger = container.querySelector('[data-state]')
      expect(trigger).toBeInTheDocument()
      expect(trigger?.textContent).toBe('Running')
    })
  })

  describe('custom className', () => {
    it('applies additional className to the badge', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} className="extra-class" />)
      const cls = getBadgeByText('Running').className
      expect(cls).toContain('extra-class')
    })

    it('preserves status classes alongside custom className', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} className="extra-class" />)
      const cls = getBadgeByText('Running').className
      expect(cls).toContain('bg-green-500/15')
      expect(cls).toContain('extra-class')
    })
  })

  describe('badge variant', () => {
    it('renders with rounded-full and border from badge base styles', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const cls = getBadgeByText('Running').className
      expect(cls).toContain('rounded-full')
      expect(cls).toContain('border')
    })
  })
})
