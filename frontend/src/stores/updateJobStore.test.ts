import { describe, it, expect, beforeEach } from 'vitest'
import { useUpdateJobStore, type UpdateJob, type UpdateJobProgressEvent, type UpdateJobCompleteEvent } from './updateJobStore'

function makeJob(overrides: Partial<UpdateJob> = {}): UpdateJob {
  return {
    id: 'job-1',
    targetType: 'container',
    targetId: 'container-abc',
    name: 'myapp_web',
    stackId: 'stack-1',
    status: 'queued',
    lines: [],
    createdAt: '2024-01-01T00:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  useUpdateJobStore.setState({ jobs: {} })
})

// ─── hydrate ────────────────────────────────────────────────────────────────

describe('hydrate', () => {
  it('replaces store with provided jobs', () => {
    const job1 = makeJob({ id: 'job-1' })
    const job2 = makeJob({ id: 'job-2', targetId: 'container-xyz' })
    useUpdateJobStore.getState().hydrate([job1, job2])

    const jobs = useUpdateJobStore.getState().jobs
    expect(Object.keys(jobs)).toHaveLength(2)
    expect(jobs['job-1']).toEqual(job1)
    expect(jobs['job-2']).toEqual(job2)
  })

  it('overwrites pre-existing jobs completely', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'old-job', name: 'old' }))
    useUpdateJobStore.getState().hydrate([makeJob({ id: 'new-job', name: 'new' })])

    const jobs = useUpdateJobStore.getState().jobs
    expect(Object.keys(jobs)).toHaveLength(1)
    expect(jobs['new-job']).toBeDefined()
    expect(jobs['old-job']).toBeUndefined()
  })

  it('hydrating with an empty array clears the store', () => {
    useUpdateJobStore.getState().upsertJob(makeJob())
    useUpdateJobStore.getState().hydrate([])

    expect(Object.keys(useUpdateJobStore.getState().jobs)).toHaveLength(0)
  })
})

// ─── applyProgress ──────────────────────────────────────────────────────────

describe('applyProgress', () => {
  it('creates a shell job when the jobId is unknown', () => {
    const evt: UpdateJobProgressEvent = {
      jobId: 'job-new',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      status: 'pulling',
    }
    useUpdateJobStore.getState().applyProgress(evt)

    const job = useUpdateJobStore.getState().jobs['job-new']
    expect(job).toBeDefined()
    expect(job.status).toBe('pulling')
    expect(job.name).toBe('myapp_web')
    expect(job.targetType).toBe('container')
    expect(job.lines).toEqual([])
  })

  it('patches status and fields when job already exists', () => {
    useUpdateJobStore.getState().upsertJob(
      makeJob({ id: 'job-1', status: 'queued', name: 'old-name', lines: [{ ts: 't', text: 'line', stream: 'stdout' }] }),
    )
    const evt: UpdateJobProgressEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'new-name',
      status: 'recreating',
    }
    useUpdateJobStore.getState().applyProgress(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.status).toBe('recreating')
    expect(job.name).toBe('new-name')
    // lines must be preserved
    expect(job.lines).toHaveLength(1)
  })
})

// ─── applyComplete ──────────────────────────────────────────────────────────

describe('applyComplete', () => {
  it('sets terminal status and error on known job', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'pulling' }))
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      status: 'error',
      error: 'pull failed',
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.status).toBe('error')
    expect(job.error).toBe('pull failed')
    expect(job.finishedAt).toBeDefined()
  })

  it('sets success status with no error', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'recreating' }))
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      status: 'success',
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.status).toBe('success')
    expect(job.error).toBeUndefined()
    expect(job.finishedAt).toBeDefined()
  })

  it('creates a shell job when jobId is unknown', () => {
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-unseen',
      targetType: 'stack',
      targetId: 'stack-1',
      stackId: 'stack-1',
      name: 'mystack',
      status: 'success',
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-unseen']
    expect(job).toBeDefined()
    expect(job.status).toBe('success')
  })
})

// ─── appendLine ─────────────────────────────────────────────────────────────

describe('appendLine', () => {
  it('appends a line to an existing job', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1' }))
    useUpdateJobStore.getState().appendLine('job-1', { ts: '2024-01-01T00:00:00Z', text: 'hello', stream: 'stdout' })
    useUpdateJobStore.getState().appendLine('job-1', { ts: '2024-01-01T00:00:01Z', text: 'world', stream: 'stderr' })

    const lines = useUpdateJobStore.getState().jobs['job-1'].lines
    expect(lines).toHaveLength(2)
    expect(lines[0].text).toBe('hello')
    expect(lines[1].text).toBe('world')
  })

  it('is a no-op for unknown jobId', () => {
    useUpdateJobStore.getState().appendLine('no-such-job', { ts: 't', text: 'x', stream: 'stdout' })
    expect(Object.keys(useUpdateJobStore.getState().jobs)).toHaveLength(0)
  })
})

// ─── jobForContainer / jobsForStack ─────────────────────────────────────────

describe('jobForContainer', () => {
  it('returns the job for a matching containerId', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', targetType: 'container', targetId: 'c-123' }))
    const result = useUpdateJobStore.getState().jobForContainer('c-123')
    expect(result?.id).toBe('job-1')
  })

  it('returns undefined when no matching container', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', targetType: 'container', targetId: 'c-123' }))
    expect(useUpdateJobStore.getState().jobForContainer('c-999')).toBeUndefined()
  })

  it('ignores stack-type jobs', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', targetType: 'stack', targetId: 's-1' }))
    expect(useUpdateJobStore.getState().jobForContainer('s-1')).toBeUndefined()
  })
})

describe('jobsForStack', () => {
  it('returns all jobs belonging to a stackId', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'j1', stackId: 'stack-A' }))
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'j2', stackId: 'stack-A', targetId: 'c-2' }))
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'j3', stackId: 'stack-B' }))

    const result = useUpdateJobStore.getState().jobsForStack('stack-A')
    expect(result).toHaveLength(2)
    expect(result.map((j) => j.id).sort()).toEqual(['j1', 'j2'])
  })

  it('returns empty array when stackId has no jobs', () => {
    expect(useUpdateJobStore.getState().jobsForStack('stack-none')).toEqual([])
  })
})

// ─── clearEvicted ────────────────────────────────────────────────────────────

describe('clearEvicted', () => {
  it('removes the specified job ids', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'j1' }))
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'j2', targetId: 'c-2' }))
    useUpdateJobStore.getState().clearEvicted(['j1'])

    const jobs = useUpdateJobStore.getState().jobs
    expect(jobs['j1']).toBeUndefined()
    expect(jobs['j2']).toBeDefined()
  })
})
