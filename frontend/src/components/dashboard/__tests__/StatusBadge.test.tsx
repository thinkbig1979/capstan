import { describe, it, expect } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { StatusBadge, type Status } from '../StatusBadge'

function getStatusByText(text: string) {
  return screen.getByText(text).closest('[data-slot="status"]') as HTMLElement
}

function getDot(badge: HTMLElement | null) {
  return badge?.querySelector('span[aria-hidden="true"]') as HTMLElement | null
}

describe('StatusBadge', () => {
  describe('renders correct label for each status', () => {
    it.each([
      ['running', 'Running'],
      ['stopped', 'Stopped'],
      ['partial', 'Partial'],
      ['error', 'Error'],
      ['unknown', 'Unknown'],
    ] as [Status, string][])('renders label "%s" for status "%s"', (status, label) => {
      renderWithProviders(<StatusBadge status={status} pulse={false} />)
      expect(screen.getByText(label)).toBeInTheDocument()
    })
  })

  describe('dot for running status', () => {
    it('renders a success-toned dot when status is running', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const badge = getStatusByText('Running')
      const dot = getDot(badge)
      expect(dot).toBeInTheDocument()
      expect(dot?.className).toContain('rounded-full')
      expect(dot?.className).toContain('bg-success')
    })

    it('dot is a small 2x2 element', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const dot = getDot(getStatusByText('Running'))
      expect(dot?.className).toContain('h-2')
      expect(dot?.className).toContain('w-2')
    })

    it('dot appears before the Running text', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const badge = getStatusByText('Running')
      const dot = getDot(badge)
      expect(dot).toBeInTheDocument()
      expect(badge.firstElementChild).toBe(dot)
    })

    it('does not render a dot for non-running statuses', () => {
      renderWithProviders(<StatusBadge status="stopped" pulse={false} />)
      const badge = getStatusByText('Stopped')
      expect(getDot(badge)).not.toBeInTheDocument()
    })
  })

  describe('pulse animation', () => {
    it('dot has animate-pulse when pulse prop is true (default)', () => {
      renderWithProviders(<StatusBadge status="running" pulse />)
      expect(getDot(getStatusByText('Running'))?.className).toContain('animate-pulse')
    })

    it('dot has animate-pulse when pulse prop is omitted (defaults to true)', () => {
      renderWithProviders(<StatusBadge status="running" />)
      expect(getDot(getStatusByText('Running'))?.className).toContain('animate-pulse')
    })

    it('dot does NOT have animate-pulse when pulse prop is false', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      expect(getDot(getStatusByText('Running'))?.className).not.toContain('animate-pulse')
    })
  })

  describe('applies correct tone classes for each status', () => {
    it('applies success tone for running', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const badge = getStatusByText('Running')
      expect(badge.getAttribute('data-tone')).toBe('success')
      expect(badge.className).toContain('text-success')
      expect(badge.className).toContain('bg-success/15')
      expect(badge.className).toContain('border-success/30')
    })

    it('applies neutral tone for stopped (intentional state, not an error)', () => {
      renderWithProviders(<StatusBadge status="stopped" pulse={false} />)
      const badge = getStatusByText('Stopped')
      expect(badge.getAttribute('data-tone')).toBe('neutral')
      expect(badge.className).toContain('text-muted-foreground')
      expect(badge.className).toContain('bg-muted')
    })

    it('applies error tone for error (broken/unreadable stack)', () => {
      renderWithProviders(<StatusBadge status="error" pulse={false} />)
      const badge = getStatusByText('Error')
      expect(badge.getAttribute('data-tone')).toBe('error')
      expect(badge.className).toContain('text-destructive')
      expect(badge.className).toContain('bg-destructive/15')
    })

    it('applies warning tone for partial', () => {
      renderWithProviders(<StatusBadge status="partial" pulse={false} />)
      const badge = getStatusByText('Partial')
      expect(badge.getAttribute('data-tone')).toBe('warning')
      expect(badge.className).toContain('text-warning')
      expect(badge.className).toContain('bg-warning/15')
    })

    it('applies neutral tone for unknown', () => {
      renderWithProviders(<StatusBadge status="unknown" pulse={false} />)
      const badge = getStatusByText('Unknown')
      expect(badge.getAttribute('data-tone')).toBe('neutral')
      expect(badge.className).toContain('text-muted-foreground')
      expect(badge.className).toContain('bg-muted')
    })
  })

  describe('falls back to unknown for invalid status values', () => {
    it('renders Unknown label for an unrecognized status', () => {
      renderWithProviders(<StatusBadge status={'bogus' as Status} pulse={false} />)
      expect(screen.getByText('Unknown')).toBeInTheDocument()
    })

    it('applies neutral tone for an unrecognized status', () => {
      renderWithProviders(<StatusBadge status={'bogus' as Status} pulse={false} />)
      const badge = getStatusByText('Unknown')
      expect(badge.getAttribute('data-tone')).toBe('neutral')
      expect(badge.className).toContain('text-muted-foreground')
    })

    it('does not render a dot for an unrecognized status', () => {
      renderWithProviders(<StatusBadge status={'bogus' as Status} pulse />)
      expect(getDot(getStatusByText('Unknown'))).not.toBeInTheDocument()
    })
  })

  describe('tooltip', () => {
    it('renders tooltip trigger wrapping the badge', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const badge = getStatusByText('Running')
      expect(badge).toHaveAttribute('data-state', 'closed')
    })
  })

  describe('custom className', () => {
    it('applies additional className to the badge', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} className="extra-class" />)
      expect(getStatusByText('Running').className).toContain('extra-class')
    })

    it('preserves status classes alongside custom className', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} className="extra-class" />)
      const cls = getStatusByText('Running').className
      expect(cls).toContain('text-success')
      expect(cls).toContain('extra-class')
    })
  })

  describe('badge base styling', () => {
    it('renders with rounded-full and border from the Status primitive', () => {
      renderWithProviders(<StatusBadge status="running" pulse={false} />)
      const cls = getStatusByText('Running').className
      expect(cls).toContain('rounded-full')
      expect(cls).toContain('border')
    })
  })
})
