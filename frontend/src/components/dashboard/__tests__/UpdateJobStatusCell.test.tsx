/**
 * Tests for UpdateJobStatusCell outcome-driven rendering.
 *
 * Key contract:
 *  - outcome='success'   → green "Updated"
 *  - outcome='no_change' → blue "Already up to date" (NOT "Updated")
 *  - outcome='failed'    → red "Failed" + retry button
 *  - non-terminal status → progress label (Queued/Pulling/Recreating)
 *  - no job             → Update/Update & Restart button
 *
 * A no_change job must NEVER render as "Updated".
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { UpdateJobStatusCell } from '../UpdateJobStatusCell'
import type { UpdateJob } from '@/stores/updateJobStore'

// Stub out the WS hook used inside JobLogPanel — we don't need real WS in unit tests.
vi.mock('@/hooks/useUpdateJobStream', () => ({
  useUpdateJobStream: vi.fn().mockReturnValue({ connected: false }),
}))

// ─── Helpers ─────────────────────────────────────────────────────────────────

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

const defaultProps = {
  expanded: false,
  onToggleExpand: vi.fn(),
  onUpdate: vi.fn(),
  isRunning: true,
  updatePending: false,
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─── No job ───────────────────────────────────────────────────────────────────

describe('UpdateJobStatusCell — no job', () => {
  it('renders the Update & Restart button when no job exists and container is running', () => {
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={undefined} />)
    expect(screen.getByRole('button', { name: /Update & Restart/i })).toBeInTheDocument()
    expect(screen.queryByText('Updated')).not.toBeInTheDocument()
    expect(screen.queryByText('Already up to date')).not.toBeInTheDocument()
  })

  it('renders "Update" (not restart) when container is stopped', () => {
    renderWithProviders(
      <UpdateJobStatusCell {...defaultProps} job={undefined} isRunning={false} />,
    )
    expect(screen.getByRole('button', { name: /^Update$/i })).toBeInTheDocument()
  })
})

// ─── In-progress states ───────────────────────────────────────────────────────

describe('UpdateJobStatusCell — in-progress states', () => {
  it('shows Queued for queued status', () => {
    const job = makeJob({ status: 'queued' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText(/Queued/i)).toBeInTheDocument()
  })

  it('shows Pulling for pulling status', () => {
    const job = makeJob({ status: 'pulling' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText(/Pulling/i)).toBeInTheDocument()
  })

  it('shows Recreating for recreating status', () => {
    const job = makeJob({ status: 'recreating' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText(/Recreating/i)).toBeInTheDocument()
  })
})

// ─── Terminal: outcome=success ────────────────────────────────────────────────

describe('UpdateJobStatusCell — outcome=success', () => {
  it('renders "Updated" for a success outcome', () => {
    const job = makeJob({ status: 'success', outcome: 'success', reason: 'image advanced' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText('Updated')).toBeInTheDocument()
    expect(screen.queryByText('Already up to date')).not.toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })
})

// ─── Terminal: outcome=no_change ──────────────────────────────────────────────

describe('UpdateJobStatusCell — outcome=no_change', () => {
  it('renders "Already up to date" for a no_change outcome', () => {
    const job = makeJob({ status: 'success', outcome: 'no_change', reason: 'digests match' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText('Already up to date')).toBeInTheDocument()
  })

  it('does NOT render "Updated" for a no_change outcome (the truthfulness gate)', () => {
    const job = makeJob({ status: 'success', outcome: 'no_change', reason: 'digests match' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    // "Updated" must never appear for a no-op — this is the key invariant
    expect(screen.queryByText('Updated')).not.toBeInTheDocument()
  })

  it('does not render a retry button for no_change (it was not a failure)', () => {
    const job = makeJob({ status: 'success', outcome: 'no_change' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.queryByRole('button', { name: /Retry/i })).not.toBeInTheDocument()
  })
})

// ─── Terminal: outcome=failed ─────────────────────────────────────────────────

describe('UpdateJobStatusCell — outcome=failed', () => {
  it('renders "Failed" for a failed outcome', () => {
    const job = makeJob({ status: 'error', outcome: 'failed', reason: 'manifest unknown' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Updated')).not.toBeInTheDocument()
    expect(screen.queryByText('Already up to date')).not.toBeInTheDocument()
  })

  it('renders a Retry button after a failed outcome', () => {
    const job = makeJob({ status: 'error', outcome: 'failed', reason: 'pull error' })
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })
})

// ─── Backward compat: no outcome field (old backend) ─────────────────────────

describe('UpdateJobStatusCell — legacy backend (no outcome field)', () => {
  it('falls back to "Updated" when status=success and outcome is absent', () => {
    const job = makeJob({ status: 'success' })
    // No outcome field — old backend response
    delete (job as Partial<UpdateJob>).outcome
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText('Updated')).toBeInTheDocument()
  })

  it('falls back to "Failed" when status=error and outcome is absent', () => {
    const job = makeJob({ status: 'error', error: 'pull failed' })
    delete (job as Partial<UpdateJob>).outcome
    renderWithProviders(<UpdateJobStatusCell {...defaultProps} job={job} />)
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })
})
