import { describe, it, expect } from 'vitest'
import { parseContainerUptime, stackUptime } from '../uptime'
import type { Container } from '@/types'

function container(overrides: Partial<Container>): Container {
  return {
    id: 'c1',
    name: 'web',
    image: 'nginx:1',
    state: 'running',
    status: 'Up 2 hours',
    ports: [],
    ...overrides,
  }
}

describe('parseContainerUptime', () => {
  it.each([
    ['Up 2 hours', 2 * 3600, '2 hours'],
    ['Up 1 second', 1, '1 second'],
    ['Up 6 days (healthy)', 6 * 86400, '6 days'],
    ['Up About a minute', 60, 'About a minute'],
    ['Up About an hour', 3600, 'About an hour'],
    ['Up 3 weeks', 3 * 604800, '3 weeks'],
  ])('parses %s', (status, seconds, label) => {
    expect(parseContainerUptime(status)).toEqual({ seconds, label })
  })

  it.each([
    'Exited (0) 3 hours ago',
    'Restarting (1) 5 seconds ago',
    'Up Less than a second',
    'Created',
  ])('returns null for %s', (status) => {
    expect(parseContainerUptime(status)).toBeNull()
  })
})

describe('stackUptime', () => {
  it('reports the youngest running container, so the chip never overstates', () => {
    const containers = [
      container({ id: 'c1', status: 'Up 6 days' }),
      container({ id: 'c2', status: 'Up 2 hours' }),
    ]
    expect(stackUptime(containers)).toBe('up 2 hours')
  })

  it('ignores non-running containers', () => {
    const containers = [
      container({ id: 'c1', status: 'Up 6 days' }),
      container({ id: 'c2', state: 'exited', status: 'Exited (0) 2 hours ago' }),
    ]
    expect(stackUptime(containers)).toBe('up 6 days')
  })

  it('returns null when nothing is running or parseable', () => {
    expect(stackUptime([])).toBeNull()
    expect(stackUptime(undefined)).toBeNull()
    expect(stackUptime([container({ state: 'exited', status: 'Exited (0)' })])).toBeNull()
  })
})
