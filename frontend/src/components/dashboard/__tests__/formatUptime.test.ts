import { describe, it, expect } from 'vitest'
import { formatUptime } from '../ContainersOverviewTab'

describe('formatUptime', () => {
  it('renders an em dash for an empty timestamp', () => {
    expect(formatUptime('')).toBe('—')
  })

  it('renders an em dash for Go zero time (D-1: was "739766d 14h")', () => {
    expect(formatUptime('0001-01-01T00:00:00Z')).toBe('—')
  })

  it('renders an em dash for an unparseable timestamp', () => {
    expect(formatUptime('not-a-date')).toBe('—')
  })

  it('renders an em dash for a future timestamp', () => {
    const future = new Date(Date.now() + 60_000).toISOString()
    expect(formatUptime(future)).toBe('—')
  })

  it('formats minutes for a recent start', () => {
    const start = new Date(Date.now() - 5 * 60_000).toISOString()
    expect(formatUptime(start)).toBe('5m')
  })

  it('formats hours and minutes', () => {
    const start = new Date(Date.now() - (3 * 60 + 12) * 60_000).toISOString()
    expect(formatUptime(start)).toBe('3h 12m')
  })

  it('formats days and hours', () => {
    const start = new Date(Date.now() - (2 * 24 + 5) * 60 * 60_000).toISOString()
    expect(formatUptime(start)).toBe('2d 5h')
  })
})
