import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { DockerCleanupCard } from '../DockerCleanupCard'

// A dangling image has a repository name only sometimes. `<none>:<none>` is the
// fully-untagged form every locally built superseded image takes — the common
// case — and `repo:<none>` is what a registry-pulled one leaves behind. The
// backend omits `repository` for the first, so a card that assumes the field is
// always there renders a blank row for the majority of real candidates.

const mockGetCleanupPolicy = vi.fn()
const mockUpdateCleanupPolicy = vi.fn()
const mockPreviewCleanup = vi.fn()
const mockGetCleanupHistory = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    getCleanupPolicy: (...args: unknown[]) => mockGetCleanupPolicy(...args),
    updateCleanupPolicy: (...args: unknown[]) => mockUpdateCleanupPolicy(...args),
    previewCleanup: (...args: unknown[]) => mockPreviewCleanup(...args),
    getCleanupHistory: (...args: unknown[]) => mockGetCleanupHistory(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

// The floors are deliberately NOT the server's current 1/1: they come off the
// wire, so a card that hardcoded the real floor would still pass against 1 and
// tell us nothing.
const POLICY = {
  enabled: false,
  minAgeHours: 168,
  intervalHours: 24,
  minAllowedAgeHours: 6,
  minAllowedIntervalHours: 2,
}

// Registry-pulled and superseded: Docker keeps the repository, drops the tag.
const REPO_CANDIDATE = {
  id: 'sha256:0123456789abcdef0123456789abcdef',
  repository: 'ghcr.io/paperless-ngx/paperless-ngx',
  size: 1024 * 1024,
  created: 1750000000,
}
const REPO_CANDIDATE_TRUNCATED_ID = '0123456789abcdef012'

// Locally built and superseded: `<none>:<none>`, so `repository` is absent.
const ID_CANDIDATE = {
  id: 'sha256:fedcba9876543210fedcba9876543210',
  size: 2 * 1024 * 1024,
  created: 1750000001,
}
const ID_CANDIDATE_TRUNCATED_ID = 'fedcba9876543210fed'

// finishedAt and errorMessage are the only optional fields on the wire, and the
// backend OMITS them rather than sending null (models.go:411, :416). The
// fixtures below omit the KEYS for that reason: `finishedAt: undefined` would
// test a shape the server never sends.
const SUCCESS_RUN = {
  id: 'run-1',
  trigger: 'scheduled',
  status: 'success',
  startedAt: '2026-09-18T10:00:00Z',
  finishedAt: '2026-09-18T10:00:30Z',
  imagesDeleted: 3,
  bytesReclaimed: 5 * 1024 * 1024,
  cacheBytesReclaimed: 2 * 1024 * 1024,
  // 72, deliberately NOT the policy's 168: minAgeHours is stored PER RUN so a
  // run stays interpretable after the policy changes, and a fixture sharing the
  // policy's number cannot tell a per-row read from a live-policy read.
  minAgeHours: 72,
}

const FAILED_RUN = {
  id: 'run-2',
  trigger: 'manual',
  status: 'failed',
  startedAt: '2026-09-18T09:00:00Z',
  imagesDeleted: 0,
  bytesReclaimed: 0,
  cacheBytesReclaimed: 0,
  minAgeHours: 72,
  errorMessage: 'Cannot connect to the Docker daemon',
}

function previewOf(candidates: Array<Record<string, unknown>>) {
  return {
    candidates,
    reclaimableBytes: candidates.reduce((sum, c) => sum + (c.size as number), 0),
    minAgeHours: POLICY.minAgeHours,
  }
}

function renderCard() {
  return render(<DockerCleanupCard />, { wrapper: createWrapper() })
}

async function clickPreview() {
  fireEvent.click(await screen.findByRole('button', { name: 'Preview' }))
}

describe('DockerCleanupCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetCleanupPolicy.mockResolvedValue(POLICY)
    mockUpdateCleanupPolicy.mockResolvedValue(POLICY)
    mockPreviewCleanup.mockResolvedValue(previewOf([]))
    // Mandatory, not cosmetic: without it the ten tests below stay green while
    // the card renders the history error sentence.
    mockGetCleanupHistory.mockResolvedValue({ runs: [], limit: 20 })
  })

  describe('run history', () => {
    it('renders a recorded run with its trigger, status and total reclaimed', async () => {
      mockGetCleanupHistory.mockResolvedValue({ runs: [SUCCESS_RUN], limit: 20 })
      renderCard()

      expect(await screen.findByText('scheduled')).toBeInTheDocument()
      expect(screen.getByText('success')).toBeInTheDocument()
      // Image bytes plus build-cache bytes: 5 MB + 2 MB. Reporting only
      // bytesReclaimed would under-report every run that cleared cache.
      expect(screen.getByText('7.00 MB')).toBeInTheDocument()
      expect(screen.getByText('3')).toBeInTheDocument()
      // The floor the run actually applied, stored per row, not the live policy,
      // which this fixture sets to 168.
      expect(screen.getByText('72 h')).toBeInTheDocument()
      expect(screen.queryByText('168 h')).toBeNull()
    })

    it('shows why a failed run failed', async () => {
      mockGetCleanupHistory.mockResolvedValue({ runs: [FAILED_RUN], limit: 20 })
      renderCard()

      expect(await screen.findByText('failed')).toBeInTheDocument()
      expect(screen.getByText('Cannot connect to the Docker daemon')).toBeInTheDocument()
    })

    it('prints no "undefined" for a run whose optional fields are absent', async () => {
      // Both keys OMITTED, exactly as the wire sends a successful run: Go's
      // omitempty drops finishedAt (a nil *string) and errorMessage (an empty
      // string) rather than sending null.
      const runWithoutOptionals = {
        id: SUCCESS_RUN.id,
        trigger: SUCCESS_RUN.trigger,
        status: SUCCESS_RUN.status,
        startedAt: SUCCESS_RUN.startedAt,
        imagesDeleted: SUCCESS_RUN.imagesDeleted,
        bytesReclaimed: SUCCESS_RUN.bytesReclaimed,
        cacheBytesReclaimed: SUCCESS_RUN.cacheBytesReclaimed,
        minAgeHours: SUCCESS_RUN.minAgeHours,
      }
      expect('finishedAt' in runWithoutOptionals).toBe(false)
      expect('errorMessage' in runWithoutOptionals).toBe(false)
      mockGetCleanupHistory.mockResolvedValue({ runs: [runWithoutOptionals], limit: 20 })
      const { container } = renderCard()

      expect(await screen.findByText('success')).toBeInTheDocument()
      expect(screen.queryByText(/undefined/)).toBeNull()
      expect(container.textContent).not.toContain('undefined')
    })

    it('says the history is empty when there are no runs', async () => {
      renderCard()

      expect(await screen.findByText('No cleanup runs yet.')).toBeInTheDocument()
      // An empty table is not an unreadable one, and the two must not share a
      // sentence: the operator's next action differs.
      expect(screen.queryByText('Run history is unavailable.')).toBeNull()
    })

    it('says the history is unreadable when the request fails', async () => {
      mockGetCleanupHistory.mockRejectedValue(new Error('boom'))
      renderCard()

      expect(await screen.findByText('Run history is unavailable.')).toBeInTheDocument()
      expect(screen.queryByText('No cleanup runs yet.')).toBeNull()
    })
  })

  describe('preview candidate labels', () => {
    it('renders the repository name, and not the image id, when the image has one', async () => {
      mockPreviewCleanup.mockResolvedValue(previewOf([REPO_CANDIDATE]))
      renderCard()
      await clickPreview()

      expect(await screen.findByText(REPO_CANDIDATE.repository)).toBeInTheDocument()
      // Negative arm: without it, a card that printed both would pass.
      expect(screen.queryByText(REPO_CANDIDATE_TRUNCATED_ID)).not.toBeInTheDocument()
      expect(screen.queryByText(REPO_CANDIDATE.id)).not.toBeInTheDocument()
    })

    it('renders the image id, prefix stripped and truncated, when the image has none', async () => {
      mockPreviewCleanup.mockResolvedValue(previewOf([ID_CANDIDATE]))
      renderCard()
      await clickPreview()

      expect(await screen.findByText(ID_CANDIDATE_TRUNCATED_ID)).toBeInTheDocument()
      // Negative arm: the id must be stripped of its algorithm prefix and
      // truncated, not printed whole. A raw slice(0, 12) would leave
      // `sha256:fedcb`, which is mostly prefix.
      expect(screen.queryByText(ID_CANDIDATE.id)).not.toBeInTheDocument()
      expect(screen.queryByText(/^sha256:/)).not.toBeInTheDocument()
    })

    it('reports how much the run would reclaim', async () => {
      mockPreviewCleanup.mockResolvedValue(previewOf([REPO_CANDIDATE, ID_CANDIDATE]))
      renderCard()
      await clickPreview()

      expect(await screen.findByText(/reclaiming 3\.00 MB/)).toBeInTheDocument()
      // The floor the server echoed, in the same sentence as the total. Without
      // it the list sits under a header naming the input's floor, which stops
      // being the floor it was computed at the moment the operator edits it.
      expect(screen.getByText(/created more than 168 hours ago/)).toBeInTheDocument()
    })
  })

  describe('the age floor', () => {
    it('disables Save below the floor and enables it at the floor', async () => {
      renderCard()
      const age = await screen.findByLabelText('Age floor (hours)')
      const save = screen.getByRole('button', { name: 'Save cleanup schedule' })

      // Below the floor the server would 400, so the form refuses first.
      fireEvent.change(age, { target: { value: '3' } })
      expect(save).toBeDisabled()
      expect(screen.getByText('The age floor must be at least 6 hours.')).toBeInTheDocument()

      // Same instrument, other side: exactly at the floor is a legal change.
      fireEvent.change(age, { target: { value: '6' } })
      expect(save).toBeEnabled()
      expect(screen.queryByText('The age floor must be at least 6 hours.')).not.toBeInTheDocument()
    })

    it('keeps Save disabled while nothing has changed', async () => {
      renderCard()
      await screen.findByLabelText('Age floor (hours)')

      expect(screen.getByRole('button', { name: 'Save cleanup schedule' })).toBeDisabled()
    })
  })

  it('previews at the age floor even when the interval is below its floor', async () => {
    renderCard()
    // The preview endpoint validates minAgeHours and nothing else, so an
    // interval the PUT would reject must not block a preview — but it must
    // still block the save.
    fireEvent.change(await screen.findByLabelText('Run every (hours)'), {
      target: { value: '1' },
    })

    expect(screen.getByRole('button', { name: 'Preview' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Save cleanup schedule' })).toBeDisabled()
  })

  it('sends only the fields that changed', async () => {
    renderCard()
    fireEvent.change(await screen.findByLabelText('Run every (hours)'), {
      target: { value: '12' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save cleanup schedule' }))

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ intervalHours: 12 })
  })

  it('sends the enabled switch when it is the only change', async () => {
    renderCard()
    fireEvent.click(await screen.findByRole('switch'))
    fireEvent.click(screen.getByRole('button', { name: 'Save cleanup schedule' }))

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ enabled: true })
  })

  it('previews without saving the policy', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([ID_CANDIDATE]))
    renderCard()
    await clickPreview()

    await waitFor(() => expect(mockPreviewCleanup).toHaveBeenCalledWith(POLICY.minAgeHours))
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()
  })

  it('refuses to render the form when the policy cannot be read', async () => {
    mockGetCleanupPolicy.mockRejectedValue(new Error('unreadable'))
    renderCard()

    expect(await screen.findByText(/could not be read/)).toBeInTheDocument()
    // No invented values, and no Save to persist them over the real policy.
    expect(screen.queryByLabelText('Age floor (hours)')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save cleanup schedule' }),
    ).not.toBeInTheDocument()
  })
})

/**
 * agent-os-wczm. Two sites in this one card. The policy guard was
 * `isError || !data` on a WRITE-BACK form, and the run-history guard was
 * `history.isError || !history.data` in a ternary — invisible to any
 * `if (isError` sweep, which is why it was missed when the bead was filed.
 *
 * Both arms resolve first and reject a REFETCH. A first-fetch-rejects fixture
 * leaves `data` undefined and stays green under either guard.
 */
describe('DockerCleanupCard — a failed REFETCH must not discard data', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetCleanupPolicy.mockResolvedValue(POLICY)
    mockUpdateCleanupPolicy.mockResolvedValue(POLICY)
    mockPreviewCleanup.mockResolvedValue(previewOf([]))
    mockGetCleanupHistory.mockResolvedValue({ runs: [SUCCESS_RUN], limit: 20 })
  })

  function renderCardWithClient() {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    const view = render(
      <QueryClientProvider client={queryClient}>
        <DockerCleanupCard />
      </QueryClientProvider>,
    )
    return { ...view, queryClient }
  }

  it('keeps the populated schedule form and the unsaved edit when a REFETCH fails', async () => {
    const { queryClient } = renderCardWithClient()

    const ageFloor = await screen.findByLabelText('Age floor (hours)')
    fireEvent.change(ageFloor, { target: { value: '240' } })
    expect(ageFloor).toHaveValue(240)

    mockGetCleanupPolicy.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: ['settings', 'docker-cleanup'] })

    await waitFor(() =>
      expect(queryClient.getQueryState(['settings', 'docker-cleanup'])?.status).toBe('error'),
    )
    expect(screen.getByLabelText('Age floor (hours)')).toHaveValue(240)
    expect(screen.getByLabelText('Run every (hours)')).toHaveValue(24)
    expect(screen.queryByText(/The cleanup schedule could not be read/)).not.toBeInTheDocument()
    expect(screen.getByText(/Could not refresh the cleanup schedule/)).toBeInTheDocument()
  })

  it('keeps the populated run-history table when a REFETCH fails', async () => {
    const { queryClient } = renderCardWithClient()

    expect(await screen.findByText('scheduled')).toBeInTheDocument()

    mockGetCleanupHistory.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: ['settings', 'docker-cleanup-history'] })

    await waitFor(() =>
      expect(
        queryClient.getQueryState(['settings', 'docker-cleanup-history'])?.status,
      ).toBe('error'),
    )
    expect(screen.getByText('scheduled')).toBeInTheDocument()
    expect(screen.getByText('7.00 MB')).toBeInTheDocument()
    expect(screen.queryByText('Run history is unavailable.')).not.toBeInTheDocument()
    expect(screen.getByText(/Could not refresh the cleanup run history/)).toBeInTheDocument()
  })
})
