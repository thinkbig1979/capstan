import { describe, it, expect } from 'vitest'
import {
  formatBytes,
  formatDuration,
  formatDurationShort,
  formatDate,
  formatDateFull,
  formatDateTimeLocal,
  formatRelativeTime,
  formatUptime,
} from '../format'

describe('formatBytes', () => {
  it('returns 0 B for zero', () => {
    expect(formatBytes(0)).toBe('0 B')
  })

  it('formats bytes', () => {
    expect(formatBytes(512)).toBe('512.00 B')
  })

  it('formats kilobytes', () => {
    expect(formatBytes(1024)).toBe('1.00 KB')
  })

  it('formats megabytes', () => {
    expect(formatBytes(1048576)).toBe('1.00 MB')
  })

  it('returns 0 B for negative', () => {
    expect(formatBytes(-1)).toBe('0 B')
  })
})

describe('formatDuration', () => {
  it('formats seconds', () => {
    expect(formatDuration(5000)).toBe('5s')
  })

  it('formats minutes and seconds', () => {
    expect(formatDuration(125000)).toBe('2m 5s')
  })

  it('formats hours and minutes', () => {
    expect(formatDuration(7500000)).toBe('2h 5m')
  })
})

describe('formatDurationShort', () => {
  it('formats milliseconds', () => {
    expect(formatDurationShort(500)).toBe('500ms')
  })

  it('formats seconds', () => {
    expect(formatDurationShort(2500)).toBe('2.5s')
  })
})

describe('formatDate', () => {
  it('returns fallback for null', () => {
    expect(formatDate(null)).toBe('-')
  })

  it('returns custom fallback', () => {
    expect(formatDate(null, 'N/A')).toBe('N/A')
  })

  it('formats ISO date string', () => {
    const result = formatDate('2026-04-29T10:30:00Z')
    expect(result).toBeTruthy()
    expect(result).not.toBe('-')
  })

  it('formats unix timestamp', () => {
    const result = formatDate(1745920200)
    expect(result).toBeTruthy()
  })
})

describe('formatDateFull', () => {
  it('returns N/A for null', () => {
    expect(formatDateFull(null)).toBe('N/A')
  })

  it('returns N/A for undefined', () => {
    expect(formatDateFull(undefined)).toBe('N/A')
  })

  it('formats date string', () => {
    const result = formatDateFull('2026-04-29T10:30:00Z')
    expect(result).toBeTruthy()
    expect(result).not.toBe('N/A')
  })
})

describe('formatDateTimeLocal', () => {
  it('formats date for datetime-local input', () => {
    const date = new Date(2026, 3, 29, 14, 30)
    const result = formatDateTimeLocal(date)
    expect(result).toBe('2026-04-29T14:30')
  })

  it('pads single digit months and hours', () => {
    const date = new Date(2026, 0, 5, 3, 7)
    const result = formatDateTimeLocal(date)
    expect(result).toBe('2026-01-05T03:07')
  })
})

describe('formatRelativeTime', () => {
  it('returns fallback for null', () => {
    expect(formatRelativeTime(null)).toBe('-')
  })

  it('returns fallback for empty string', () => {
    expect(formatRelativeTime('')).toBe('-')
  })

  it('returns seconds ago for recent dates', () => {
    const now = new Date()
    const tenSecondsAgo = new Date(now.getTime() - 10000).toISOString()
    const result = formatRelativeTime(tenSecondsAgo)
    expect(result).toContain('s ago')
  })

  it('returns minutes ago', () => {
    const now = new Date()
    const fiveMinAgo = new Date(now.getTime() - 300000).toISOString()
    const result = formatRelativeTime(fiveMinAgo)
    expect(result).toContain('m ago')
  })

  it('returns hours ago', () => {
    const now = new Date()
    const threeHoursAgo = new Date(now.getTime() - 10800000).toISOString()
    const result = formatRelativeTime(threeHoursAgo)
    expect(result).toContain('h ago')
  })

  it('returns days ago', () => {
    const now = new Date()
    const fiveDaysAgo = new Date(now.getTime() - 432000000).toISOString()
    const result = formatRelativeTime(fiveDaysAgo)
    expect(result).toContain('d ago')
  })
})

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
