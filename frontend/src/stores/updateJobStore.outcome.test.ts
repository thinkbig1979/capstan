/**
 * Tests for the outcome/reason fields added to UpdateJob as part of the
 * Action Truth Contract (B1 Updates front-end implementation).
 *
 * Verifies:
 *  - outcome and reason survive upsertJob / hydrate round-trips
 *  - applyComplete carries outcome/reason into the store
 *  - setOutcome writes the outcome/reason without touching other fields
 *  - a no_change job must NOT be mistakable for a success job
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { useUpdateJobStore, type UpdateJob, type UpdateJobCompleteEvent } from './updateJobStore'

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

// ─── upsertJob / hydrate round-trip ─────────────────────────────────────────

describe('upsertJob preserves outcome and reason', () => {
  it('stores outcome=success and reason through upsertJob', () => {
    const job = makeJob({ status: 'success', outcome: 'success', reason: 'image digest advanced' })
    useUpdateJobStore.getState().upsertJob(job)
    const stored = useUpdateJobStore.getState().jobs['job-1']
    expect(stored.outcome).toBe('success')
    expect(stored.reason).toBe('image digest advanced')
  })

  it('stores outcome=no_change through upsertJob', () => {
    const job = makeJob({ status: 'success', outcome: 'no_change', reason: 'digests match' })
    useUpdateJobStore.getState().upsertJob(job)
    const stored = useUpdateJobStore.getState().jobs['job-1']
    expect(stored.outcome).toBe('no_change')
    expect(stored.reason).toBe('digests match')
  })

  it('hydrate preserves outcome/reason on terminal jobs', () => {
    const success = makeJob({ id: 'j1', status: 'success', outcome: 'success', reason: 'ok' })
    const noChange = makeJob({ id: 'j2', status: 'success', outcome: 'no_change', reason: 'already current' })
    useUpdateJobStore.getState().hydrate([success, noChange])

    expect(useUpdateJobStore.getState().jobs['j1'].outcome).toBe('success')
    expect(useUpdateJobStore.getState().jobs['j2'].outcome).toBe('no_change')
    expect(useUpdateJobStore.getState().jobs['j2'].reason).toBe('already current')
  })
})

// ─── applyComplete carries outcome/reason ───────────────────────────────────

describe('applyComplete carries outcome and reason', () => {
  it('sets outcome=success and reason from the complete event', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'pulling' }))
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      status: 'success',
      outcome: 'success',
      reason: 'image updated to sha256:abc',
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.status).toBe('success')
    expect(job.outcome).toBe('success')
    expect(job.reason).toBe('image updated to sha256:abc')
    expect(job.finishedAt).toBeDefined()
  })

  it('sets outcome=no_change (must NOT be confused with success)', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'pulling' }))
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      // Backend maps no_change to status='success' (the job did not error),
      // but the typed outcome distinguishes it.
      status: 'success',
      outcome: 'no_change',
      reason: 'digests already match',
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    // The job did not error — status is 'success' from the job runner's perspective
    expect(job.status).toBe('success')
    // But the typed outcome must be no_change — the image was NOT actually pulled/updated
    expect(job.outcome).toBe('no_change')
    expect(job.outcome).not.toBe('success')
    expect(job.reason).toBe('digests already match')
  })

  it('sets outcome=failed with reason', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'pulling' }))
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      status: 'error',
      outcome: 'failed',
      reason: 'manifest unknown',
      error: 'manifest unknown',
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.outcome).toBe('failed')
    expect(job.reason).toBe('manifest unknown')
  })

  it('does not set outcome when the event omits it (backward compat)', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'pulling' }))
    const evt: UpdateJobCompleteEvent = {
      jobId: 'job-1',
      targetType: 'container',
      targetId: 'container-abc',
      stackId: 'stack-1',
      name: 'myapp_web',
      status: 'success',
      // outcome/reason absent — old backend
    }
    useUpdateJobStore.getState().applyComplete(evt)

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.status).toBe('success')
    expect(job.outcome).toBeUndefined()
    expect(job.reason).toBeUndefined()
  })
})

// ─── setOutcome ──────────────────────────────────────────────────────────────

describe('setOutcome', () => {
  it('sets outcome and reason without touching status or lines', () => {
    const line = { ts: 't', text: 'line', stream: 'stdout' as const }
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'success', lines: [line] }))
    useUpdateJobStore.getState().setOutcome('job-1', 'no_change', 'digests match')

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.outcome).toBe('no_change')
    expect(job.reason).toBe('digests match')
    // status and lines must not have changed
    expect(job.status).toBe('success')
    expect(job.lines).toHaveLength(1)
  })

  it('sets outcome without reason when reason is omitted', () => {
    useUpdateJobStore.getState().upsertJob(makeJob({ id: 'job-1', status: 'success' }))
    useUpdateJobStore.getState().setOutcome('job-1', 'success')

    const job = useUpdateJobStore.getState().jobs['job-1']
    expect(job.outcome).toBe('success')
    expect(job.reason).toBeUndefined()
  })

  it('is a no-op for unknown jobId', () => {
    useUpdateJobStore.getState().setOutcome('no-such-job', 'success')
    expect(Object.keys(useUpdateJobStore.getState().jobs)).toHaveLength(0)
  })
})
