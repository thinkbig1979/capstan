import { describe, it, expect } from 'vitest'
import { parseLogMessage } from '@/components/stack/logviewer/useLogStream'
import { parseMetricsMessage } from '@/hooks/useMetricsBase'
import { parseStackEvent } from '@/hooks/useStackEvents'
import { parseJobStreamFrame } from '@/hooks/useUpdateJobStream'
import { arrayOf, frameValidator, num, omitemptyStr, record } from '@/lib/wsFrames'

// agent-os-r4kf. Fixtures are the JSON encoding/json produces for the named Go
// struct, omitempty keys absent where Go holds "". Each validator must accept
// every shape a producer sends today (criterion 4) and reject a frame whose
// declared-string field is a number (criterion 1).

describe('wsFrames readers', () => {
  it('omitemptyStr reads an absent key as the empty string Go held', () => {
    expect(omitemptyStr(undefined)).toBe('')
  })

  it('frameValidator turns a failed read into null', () => {
    const v = frameValidator((raw) => num(record(raw).n))
    expect(v({ n: 1 })).toBe(1)
    expect(v({ n: '1' })).toBeNull()
    expect(v(null)).toBeNull()
    expect(v([])).toBeNull()
  })

  it('frameValidator rethrows an error that is not a failed read', () => {
    const v = frameValidator(() => {
      throw new TypeError('reader bug')
    })
    expect(() => v({})).toThrow('reader bug')
  })

  it('arrayOf is all-or-nothing', () => {
    const v = frameValidator((raw) => arrayOf(raw, num))
    expect(v([1, 2])).toEqual([1, 2])
    expect(v([1, '2'])).toBeNull()
  })
})

describe('parseLogMessage (handlers.LogLine)', () => {
  const line = { container: 'web-1', timestamp: '2026-09-23T10:00:00Z', message: 'ready' }

  it('accepts the Go shape', () => {
    expect(parseLogMessage(line)).toEqual(line)
  })

  it('rejects a numeric message', () => {
    expect(parseLogMessage({ ...line, message: 42 })).toBeNull()
  })
})

describe('parseMetricsMessage (handlers.MetricsFrame)', () => {
  const container = {
    containerId: 'c1', name: 'web', cpuPercent: 1.5, memUsage: 10, memLimit: 100, memPercent: 10,
    netRx: 0, netTx: 0, blockRead: 0, blockWrite: 0, memSwap: 0, pids: 3,
  }

  it('accepts a frame with containers', () => {
    const frame = { timestamp: '2026-09-23T10:00:00Z', containers: [container] }
    expect(parseMetricsMessage(frame)).toEqual(frame)
  })

  it('accepts the empty and the null containers a Go slice can marshal to', () => {
    expect(parseMetricsMessage({ timestamp: 't', containers: [] })).toEqual({ timestamp: 't', containers: [] })
    expect(parseMetricsMessage({ timestamp: 't', containers: null })).toEqual({ timestamp: 't', containers: null })
  })

  it('rejects the whole frame when one container has a string name field as a number', () => {
    expect(parseMetricsMessage({ timestamp: 't', containers: [container, { ...container, name: 7 }] })).toBeNull()
  })
})

describe('parseStackEvent (models.StackEvent)', () => {
  const ts = '2026-09-23T10:00:00Z'

  it('accepts a stack_status, including the paused status stackEventFor emits', () => {
    for (const status of ['running', 'stopped', 'paused']) {
      expect(parseStackEvent({ type: 'stack_status', stackId: 's1', containerId: 'c1', event: 'pause', status, timestamp: ts }))
        .toEqual({ type: 'stack_status', stackId: 's1', status, timestamp: ts })
    }
  })

  it('accepts an unassociated container_event, reading the omitted stackId as ""', () => {
    // monitor.go unassociatedStackEvent: StackID "" is omitted from the JSON.
    expect(parseStackEvent({ type: 'container_event', containerId: 'c1', event: 'die', timestamp: ts }))
      .toEqual({ type: 'container_event', stackId: '', containerId: 'c1', event: 'die', timestamp: ts })
  })

  it('keeps optional fields absent on a bare resource_changed', () => {
    const parsed = parseStackEvent({ type: 'resource_changed', timestamp: ts })
    expect(parsed).toEqual({ type: 'resource_changed', timestamp: ts })
    expect(parsed && 'event' in parsed ? parsed.event : undefined).toBeUndefined()
  })

  it('accepts the signal-only events', () => {
    for (const type of ['update_scan_complete', 'update_scan_failed', 'update_policy_changed', 'updates_changed', 'backup_policy_changed']) {
      expect(parseStackEvent({ type, timestamp: ts })).toEqual({ type, timestamp: ts })
    }
    expect(parseStackEvent({ type: 'update_completed', containerId: 'c1', timestamp: ts }))
      .toEqual({ type: 'update_completed', containerId: 'c1', timestamp: ts })
  })

  it('accepts update_job_progress for a standalone container, whose stackId Go omits', () => {
    // updates.go: a container job's JobSpec.StackID is "" when no stack owns it.
    expect(parseStackEvent({
      type: 'update_job_progress', jobId: 'j1', targetType: 'container', targetId: 'c1',
      name: 'web', event: 'pulling', status: 'pulling', timestamp: ts,
    })).toEqual({
      type: 'update_job_progress', jobId: 'j1', targetType: 'container', targetId: 'c1',
      stackId: '', name: 'web', status: 'pulling',
    })
  })

  it('accepts update_job_complete with and without its outcome', () => {
    const base = { type: 'update_job_complete', jobId: 'j1', targetType: 'stack', targetId: 's1', stackId: 's1', name: 'app', status: 'success', timestamp: ts }
    expect(parseStackEvent({ ...base, outcome: 'no_change', reason: 'Already up to date' }))
      .toMatchObject({ outcome: 'no_change', reason: 'Already up to date' })
    expect(parseStackEvent(base)).toMatchObject({ status: 'success', outcome: undefined })
  })

  it('rejects a numeric stackId', () => {
    expect(parseStackEvent({ type: 'stack_status', stackId: 5, status: 'running', timestamp: ts })).toBeNull()
  })

  it('rejects a type the union does not declare', () => {
    expect(parseStackEvent({ type: 'something_new', timestamp: ts })).toBeNull()
  })
})

describe('parseJobStreamFrame (handlers.wsJobFrame)', () => {
  const job = {
    id: 'j1', targetType: 'container', targetId: 'c1', name: 'web', stackId: '', status: 'queued',
    lines: [], createdAt: '2026-09-23T10:00:00Z',
  }
  const line = { ts: '2026-09-23T10:00:01Z', text: 'pulling', stream: 'stdout' }

  it('accepts every frame the Go writers send', () => {
    expect(parseJobStreamFrame({ type: 'snapshot', job })).toEqual({ type: 'snapshot', job })
    expect(parseJobStreamFrame({ type: 'snapshot', job: { ...job, status: 'success', lines: [line], outcome: 'success', startedAt: 'a', finishedAt: 'b' } }))
      .toMatchObject({ job: { outcome: 'success', lines: [line] } })
    expect(parseJobStreamFrame({ type: 'line', line })).toEqual({ type: 'line', line })
    expect(parseJobStreamFrame({ type: 'status', status: 'recreating' })).toEqual({ type: 'status', status: 'recreating' })
    expect(parseJobStreamFrame({ type: 'done', status: 'error', error: 'boom', outcome: 'failed', reason: 'boom' }))
      .toEqual({ type: 'done', status: 'error', error: 'boom', outcome: 'failed', reason: 'boom' })
    expect(parseJobStreamFrame({ type: 'error', error: 'job not found' })).toEqual({ type: 'error', error: 'job not found' })
  })

  it('rejects a line whose text is a number', () => {
    expect(parseJobStreamFrame({ type: 'line', line: { ...line, text: 42 } })).toBeNull()
  })

  it('rejects a snapshot whose job has a malformed line', () => {
    expect(parseJobStreamFrame({ type: 'snapshot', job: { ...job, lines: [{ ...line, stream: 'other' }] } })).toBeNull()
  })

  it('rejects an unknown frame type', () => {
    expect(parseJobStreamFrame({ type: 'something-new' })).toBeNull()
  })
})
