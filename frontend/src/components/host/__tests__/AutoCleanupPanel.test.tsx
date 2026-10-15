import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { toast } from 'sonner'
import { queryKeys } from '@/lib/query-keys'
import { AutoCleanupPanel } from '../AutoCleanupPanel'

// Panel behaviour (agent-os-6qfz, spec AC8), plus the preview, history and
// run-now tests moved here from settings/__tests__/DockerCleanupCard.test.tsx.

const mockGetCleanupPolicy = vi.fn()
const mockUpdateCleanupPolicy = vi.fn()
const mockPreviewCleanup = vi.fn()
const mockGetCleanupHistory = vi.fn()
const mockRunCleanup = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    getCleanupPolicy: (...args: unknown[]) => mockGetCleanupPolicy(...args),
    updateCleanupPolicy: (...args: unknown[]) => mockUpdateCleanupPolicy(...args),
    previewCleanup: (...args: unknown[]) => mockPreviewCleanup(...args),
    getCleanupHistory: (...args: unknown[]) => mockGetCleanupHistory(...args),
    runCleanup: (...args: unknown[]) => mockRunCleanup(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const HOUR = 3_600_000
const MB = 1024 * 1024
const isoFromNow = (ms: number) => new Date(Date.now() + ms).toISOString()

const CATEGORIES = {
  danglingImages: true,
  unusedImages: false,
  buildCache: true,
  containers: false,
  volumes: false,
  networks: false,
}

// 168, not the mockup's 48: every number in the copy must come from here.
const POLICY = {
  enabled: true,
  minAgeHours: 168,
  intervalHours: 24,
  categories: CATEGORIES,
  alertThresholdBytes: 5 * 1024 ** 3,
  nextRunAt: isoFromNow(5 * HOUR + 60_000),
  minAllowedAgeHours: 6,
  minAllowedIntervalHours: 2,
}

const SUCCESS_RUN = {
  id: 'run-1',
  trigger: 'scheduled',
  status: 'success',
  startedAt: isoFromNow(-3 * HOUR),
  finishedAt: isoFromNow(-3 * HOUR + 30_000),
  imagesDeleted: 3,
  bytesReclaimed: 5 * MB,
  cacheBytesReclaimed: 2 * MB,
  // 72, deliberately NOT the policy's 168: minAgeHours is stored PER RUN.
  minAgeHours: 72,
  categories: 'dangling_images,build_cache',
  containersDeleted: 0,
  volumesDeleted: 0,
  networksDeleted: 0,
  otherBytesReclaimed: 0,
}

const FAILED_RUN = {
  ...SUCCESS_RUN,
  id: 'run-2',
  trigger: 'manual',
  status: 'failed',
  finishedAt: undefined,
  imagesDeleted: 0,
  bytesReclaimed: 0,
  cacheBytesReclaimed: 0,
  errorMessage: 'Cannot connect to the Docker daemon',
}

const SKIPPED_RUN = {
  ...FAILED_RUN,
  id: 'run-3',
  trigger: 'scheduled',
  status: 'skipped',
  errorMessage: 'stack "paperless" was busy (deploy)',
}

// Registry-pulled and superseded: Docker keeps the repository, drops the tag.
const REPO_CANDIDATE = {
  id: 'sha256:0123456789abcdef0123456789abcdef',
  repository: 'ghcr.io/paperless-ngx/paperless-ngx',
  size: MB,
  created: 1750000000,
}
const REPO_CANDIDATE_TRUNCATED_ID = '0123456789abcdef012'

// Locally built and superseded: `<none>:<none>`, so `repository` is absent.
const ID_CANDIDATE = {
  id: 'sha256:fedcba9876543210fedcba9876543210',
  size: 2 * MB,
  created: 1750000001,
}
const ID_CANDIDATE_TRUNCATED_ID = 'fedcba9876543210fed'

// An unused tagged image (D98): the tag is the label.
const TAGGED_CANDIDATE = {
  id: 'sha256:aaaa000000000000000000000000000000',
  tag: 'capstan:0.22.0',
  size: 4 * MB,
  reclaimableSize: 4 * MB,
  created: 1750000002,
}

function previewOf(candidates: Array<Record<string, unknown>>, extra: Record<string, unknown> = {}) {
  return {
    candidates,
    reclaimableBytes: candidates.reduce((sum, c) => sum + (c.size as number), 0),
    cacheReclaimableBytes: 0,
    containers: [],
    volumes: [],
    networks: [],
    otherReclaimableBytes: 0,
    unknownSizeCount: 0,
    minAgeHours: POLICY.minAgeHours,
    ...extra,
  }
}

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/host']}>
        <AutoCleanupPanel />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...view, queryClient, invalidate }
}

async function openPreview() {
  fireEvent.click(await screen.findByRole('button', { name: 'Preview next run' }))
  return screen.findByRole('dialog', { name: 'Preview next run' })
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetCleanupPolicy.mockResolvedValue(POLICY)
  mockUpdateCleanupPolicy.mockResolvedValue(POLICY)
  mockPreviewCleanup.mockResolvedValue(previewOf([]))
  // Mandatory, not cosmetic: without it the tests below render the history
  // error sentence and still pass the parts they do not look at.
  mockGetCleanupHistory.mockResolvedValue({ runs: [], limit: 20 })
  mockRunCleanup.mockResolvedValue({ ...SUCCESS_RUN, id: 'run-9', trigger: 'manual', minAgeHours: 168 })
})

describe('AutoCleanupPanel: status copy (AC8)', () => {
  it('is the automatic-cleanup anchor', async () => {
    const { container } = renderPanel()
    await screen.findByText('On')
    expect(container.querySelector('#automatic-cleanup')).not.toBeNull()
  })

  it('builds the sentence from the policy: 168 h, not a hardcoded number', async () => {
    renderPanel()
    const copy = await screen.findByTestId('auto-cleanup-copy')
    expect(copy).toHaveTextContent(
      'Every 24 h removes dangling images and build cache older than 168 h.',
    )
    expect(copy).toHaveTextContent(
      'Never removed automatically: unused tagged images, stopped containers, volumes, networks.',
    )
    expect(copy.textContent).not.toContain('48 h')
  })

  it('turning containers on moves "stopped containers" into the first sentence', async () => {
    mockGetCleanupPolicy.mockResolvedValue({
      ...POLICY,
      intervalHours: 12,
      minAgeHours: 72,
      categories: { ...CATEGORIES, containers: true },
    })
    renderPanel()
    const copy = await screen.findByTestId('auto-cleanup-copy')
    expect(copy).toHaveTextContent(
      'Every 12 h removes dangling images, build cache and stopped containers older than 72 h.',
    )
    expect(copy).toHaveTextContent('Never removed automatically: unused tagged images, volumes, networks.')
  })

  it('Off shows "Off" and a "Turn on" that opens Edit schedule with the switch on', async () => {
    mockGetCleanupPolicy.mockResolvedValue({ ...POLICY, enabled: false, nextRunAt: undefined })
    renderPanel()
    expect(await screen.findByText('Off')).toBeInTheDocument()
    expect(screen.queryByTestId('auto-cleanup-copy')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Turn on' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit schedule' })
    expect(within(dialog).getByRole('switch', { name: 'Run cleanup on a schedule' })).toBeChecked()
  })

  it('On shows "On" and no "Turn on"', async () => {
    renderPanel()
    expect(await screen.findByText('On')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Turn on' })).toBeNull()
  })

  it('refuses a made-up schedule when the policy cannot be read', async () => {
    mockGetCleanupPolicy.mockRejectedValue(new Error('unreadable'))
    renderPanel()
    expect(await screen.findByText(/Could not load the cleanup schedule/)).toBeInTheDocument()
    expect(screen.queryByText('Off')).toBeNull()
    expect(screen.queryByText('On')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull()
  })
})

describe('AutoCleanupPanel: last and next run (AC8)', () => {
  it('says "Never run" when there is no history', async () => {
    renderPanel()
    expect(await screen.findByText('Never run')).toBeInTheDocument()
  })

  it('freed is the sum of the three byte fields of the newest row', async () => {
    mockGetCleanupHistory.mockResolvedValue({
      runs: [{ ...SUCCESS_RUN, otherBytesReclaimed: 3 * MB }, FAILED_RUN],
      limit: 20,
    })
    renderPanel()
    // 5 + 2 + 3 MB. Any one field alone, or two of them, is a different number.
    expect(await screen.findByText('Last run 3h ago freed 10.00 MB')).toBeInTheDocument()
  })

  it('a skipped newest row reads "skipped: <reason>"', async () => {
    mockGetCleanupHistory.mockResolvedValue({ runs: [SKIPPED_RUN, SUCCESS_RUN], limit: 20 })
    renderPanel()
    expect(
      await screen.findByText('Last run 3h ago skipped: stack "paperless" was busy (deploy)'),
    ).toBeInTheDocument()
  })

  it('a failed newest row reads "failed: <reason>"', async () => {
    mockGetCleanupHistory.mockResolvedValue({ runs: [FAILED_RUN], limit: 20 })
    renderPanel()
    expect(
      await screen.findByText('Last run 3h ago failed: Cannot connect to the Docker daemon'),
    ).toBeInTheDocument()
  })

  it('next run comes from nextRunAt', async () => {
    renderPanel()
    expect(await screen.findByTestId('auto-cleanup-runs')).toHaveTextContent('next run in 5h')
  })

  it('no next run when the policy carries no nextRunAt', async () => {
    mockGetCleanupPolicy.mockResolvedValue({ ...POLICY, nextRunAt: undefined })
    renderPanel()
    await screen.findByText('Never run')
    expect(screen.getByTestId('auto-cleanup-runs')).not.toHaveTextContent('next run')
  })
})

describe('AutoCleanupPanel: Edit schedule is a dialog (real Radix)', () => {
  // The trap this pins: a confirmation rendered OUTSIDE the modal Radix Dialog
  // (useConfirm's ConfirmDialog mounted from the panel, or portalled to body)
  // is hidden from the user by the modal. Measured 2026-10-15 with that mutant:
  // this test goes red with "Unable to find ... 'Turn on volume removal'",
  // since Radix aria-hides everything outside the modal. useConfirm rendered
  // INSIDE the dialog content passed, so the trap is about placement.
  it('the volumes confirmation is clickable inside the dialog and saves', async () => {
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Edit schedule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit schedule' })

    await user.click(within(dialog).getByRole('switch', { name: 'Unattached volumes' }))
    await user.click(within(dialog).getByRole('button', { name: 'Save cleanup schedule' }))
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole('button', { name: 'Turn on volume removal' }))
    await waitFor(() =>
      expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ categories: { volumes: true } }),
    )
    // Saved: the dialog closes.
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Edit schedule' })).toBeNull())
  })

  it('Cancel closes the dialog and saves nothing', async () => {
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Edit schedule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit schedule' })
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Edit schedule' })).toBeNull())
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()
  })

  it('a save refreshes the policy and the stored-policy preview', async () => {
    const { invalidate } = renderPanel()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit schedule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit schedule' })
    fireEvent.change(within(dialog).getByLabelText('Run every (hours)'), { target: { value: '12' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save cleanup schedule' }))

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ intervalHours: 12 }))
    for (const key of [queryKeys.settings.dockerCleanup(), queryKeys.resources.cleanupPreview()]) {
      await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: key }))
    }
  })
})

describe('AutoCleanupPanel: Preview next run', () => {
  it('previews the stored policy: sends no floor and saves nothing', async () => {
    renderPanel()
    await openPreview()

    await waitFor(() => expect(mockPreviewCleanup).toHaveBeenCalledTimes(1))
    expect(mockPreviewCleanup.mock.calls[0]).toEqual([])
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()
  })

  it('renders the repository name, and not the image id, when the image has one', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([REPO_CANDIDATE]))
    renderPanel()
    await openPreview()

    expect(await screen.findByText(REPO_CANDIDATE.repository)).toBeInTheDocument()
    // Negative arm: without it, a list that printed both would pass.
    expect(screen.queryByText(REPO_CANDIDATE_TRUNCATED_ID)).not.toBeInTheDocument()
    expect(screen.queryByText(REPO_CANDIDATE.id)).not.toBeInTheDocument()
  })

  it('renders the image id, prefix stripped and truncated, when the image has none', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([ID_CANDIDATE]))
    renderPanel()
    await openPreview()

    expect(await screen.findByText(ID_CANDIDATE_TRUNCATED_ID)).toBeInTheDocument()
    // A raw slice(0, 12) would leave `sha256:fedcb`, which is mostly prefix.
    expect(screen.queryByText(ID_CANDIDATE.id)).not.toBeInTheDocument()
    expect(screen.queryByText(/^sha256:/)).not.toBeInTheDocument()
  })

  it('renders an unused tagged image by its tag', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([TAGGED_CANDIDATE]))
    renderPanel()
    await openPreview()

    expect(await screen.findByText('capstan:0.22.0')).toBeInTheDocument()
    expect(screen.queryByText(/^aaaa0000/)).not.toBeInTheDocument()
  })

  it('reports how much the run would reclaim, at the floor the server echoed', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([REPO_CANDIDATE, ID_CANDIDATE]))
    renderPanel()
    await openPreview()

    expect(await screen.findByText(/reclaiming 3\.00 MB/)).toBeInTheDocument()
    expect(screen.getByText(/created more than 168 hours ago/)).toBeInTheDocument()
  })

  describe('the build cache a run would also prune (agent-os-nacp)', () => {
    it('shows the cache figure beside the image total', async () => {
      mockPreviewCleanup.mockResolvedValue(
        previewOf([REPO_CANDIDATE, ID_CANDIDATE], { cacheReclaimableBytes: 5 * MB }),
      )
      renderPanel()
      await openPreview()

      expect(await screen.findByText(/reclaiming 3\.00 MB/)).toBeInTheDocument()
      expect(screen.getByTestId('cleanup-cache-reclaimable')).toHaveTextContent(
        'Unused build cache older than 168 hours: 5.00 MB.',
      )
    })

    it('reports cache with no images, beside the nothing-to-remove line', async () => {
      mockPreviewCleanup.mockResolvedValue(previewOf([], { cacheReclaimableBytes: 5 * MB }))
      renderPanel()
      await openPreview()

      expect(await screen.findByTestId('cleanup-cache-reclaimable')).toHaveTextContent('5.00 MB')
      expect(screen.getByText(/No image to remove/)).toBeInTheDocument()
    })

    it('prints no cache line when a run would prune none', async () => {
      mockPreviewCleanup.mockResolvedValue(previewOf([REPO_CANDIDATE, ID_CANDIDATE]))
      renderPanel()
      await openPreview()

      expect(await screen.findByText(/reclaiming 3\.00 MB/)).toBeInTheDocument()
      expect(screen.queryByTestId('cleanup-cache-reclaimable')).not.toBeInTheDocument()
    })
  })

  it('lists containers, volumes and networks, with their bytes and unknown sizes', async () => {
    mockPreviewCleanup.mockResolvedValue(
      previewOf([], {
        containers: [{ id: 'c1', label: 'old-job', detail: 'exited 9d ago', size: MB }],
        volumes: [{ id: 'v1', label: 'scratch' }],
        networks: [{ id: 'n1', label: 'test_net' }],
        otherReclaimableBytes: MB,
        unknownSizeCount: 1,
      }),
    )
    renderPanel()
    const dialog = await openPreview()

    expect(await within(dialog).findByText('old-job · exited 9d ago')).toBeInTheDocument()
    expect(within(dialog).getByText('scratch')).toBeInTheDocument()
    expect(within(dialog).getByText('size unknown')).toBeInTheDocument()
    expect(within(dialog).getByText('test_net')).toBeInTheDocument()
    expect(within(dialog).getByTestId('cleanup-other-reclaimable')).toHaveTextContent(
      'Containers and volumes: 1.00 MB.',
    )
    expect(within(dialog).getByTestId('cleanup-unknown-size')).toHaveTextContent('1 item of unknown size')
  })

  it('shows no other-bytes or unknown-size line when there are none', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([REPO_CANDIDATE]))
    renderPanel()
    const dialog = await openPreview()
    await within(dialog).findByText(REPO_CANDIDATE.repository)

    expect(within(dialog).queryByTestId('cleanup-other-reclaimable')).toBeNull()
    expect(within(dialog).queryByTestId('cleanup-unknown-size')).toBeNull()
    expect(within(dialog).queryByTestId('cleanup-preview-withheld')).toBeNull()
  })

  it('names the withheld stacks in plain words', async () => {
    mockPreviewCleanup.mockResolvedValue(previewOf([ID_CANDIDATE], { withheldStacks: ['paperless', 'immich'] }))
    renderPanel()
    const dialog = await openPreview()

    expect(await within(dialog).findByTestId('cleanup-preview-withheld')).toHaveTextContent(
      'Capstan could not read the compose file of paperless and immich.',
    )
  })
})

describe('AutoCleanupPanel: run history', () => {
  it('renders a recorded run with its trigger, status, what it removed and total freed', async () => {
    mockGetCleanupHistory.mockResolvedValue({ runs: [SUCCESS_RUN], limit: 20 })
    renderPanel()

    expect(await screen.findByText('scheduled')).toBeInTheDocument()
    expect(screen.getByText('success')).toBeInTheDocument()
    // Image bytes plus build-cache bytes: 5 MB + 2 MB.
    expect(screen.getByText('7.00 MB')).toBeInTheDocument()
    expect(screen.getByText('3 images')).toBeInTheDocument()
    // The floor the run actually applied, stored per row, not the live policy's 168.
    expect(screen.getByText('72 h')).toBeInTheDocument()
    expect(screen.queryByText('168 h')).toBeNull()
  })

  it('counts every kind a run removed and every byte field it freed', async () => {
    mockGetCleanupHistory.mockResolvedValue({
      runs: [{ ...SUCCESS_RUN, containersDeleted: 2, volumesDeleted: 1, networksDeleted: 0, otherBytesReclaimed: 3 * MB }],
      limit: 20,
    })
    renderPanel()

    expect(await screen.findByText('3 images, 2 containers, 1 volume')).toBeInTheDocument()
    expect(screen.getByText('10.00 MB')).toBeInTheDocument()
  })

  it('shows why a failed run failed', async () => {
    mockGetCleanupHistory.mockResolvedValue({ runs: [FAILED_RUN], limit: 20 })
    renderPanel()

    expect(await screen.findByText('failed')).toBeInTheDocument()
    expect(screen.getByText('Cannot connect to the Docker daemon')).toBeInTheDocument()
  })

  it('shows a skipped run in plain words, with the reason', async () => {
    mockGetCleanupHistory.mockResolvedValue({ runs: [SKIPPED_RUN], limit: 20 })
    renderPanel()

    expect(await screen.findByText('skipped')).toBeInTheDocument()
    expect(
      screen.getByText('Nothing was removed: stack "paperless" was busy (deploy)'),
    ).toBeInTheDocument()
  })

  // agent-os-8fw2: a scheduled pass that never ran is stored with minAgeHours 0.
  it('shows no number for the age floor of a run that applied none', async () => {
    const unstartedRun = {
      ...FAILED_RUN,
      id: 'run-4',
      trigger: 'scheduled',
      minAgeHours: 0,
      errorMessage: 'scheduled Docker cleanup did not run: the cleanup policy could not be read',
    }
    mockGetCleanupHistory.mockResolvedValue({ runs: [SUCCESS_RUN, unstartedRun], limit: 20 })
    renderPanel()

    expect(await screen.findByText('the cleanup policy could not be read', { exact: false, selector: 'p.mt-1' })).toBeInTheDocument()
    expect(screen.queryByText('0 h')).toBeNull()
    expect(screen.getByText('—')).toBeInTheDocument()
    expect(screen.getByText('72 h')).toBeInTheDocument()
  })

  it('prints no "undefined" for a run whose optional fields are absent', async () => {
    // Both keys OMITTED, exactly as the wire sends a successful run.
    const { finishedAt: _f, ...runWithoutOptionals } = SUCCESS_RUN
    expect('finishedAt' in runWithoutOptionals).toBe(false)
    expect('errorMessage' in runWithoutOptionals).toBe(false)
    mockGetCleanupHistory.mockResolvedValue({ runs: [runWithoutOptionals], limit: 20 })
    const { container } = renderPanel()

    expect(await screen.findByText('success')).toBeInTheDocument()
    expect(screen.queryByText(/undefined/)).toBeNull()
    expect(container.textContent).not.toContain('undefined')
  })

  it('says the history is empty when there are no runs', async () => {
    renderPanel()

    expect(await screen.findByText('No cleanup runs yet.')).toBeInTheDocument()
    expect(screen.queryByText('Run history is unavailable.')).toBeNull()
  })

  it('says the history is unreadable when the request fails', async () => {
    mockGetCleanupHistory.mockRejectedValue(new Error('boom'))
    renderPanel()

    expect(await screen.findByText('Run history is unavailable.')).toBeInTheDocument()
    expect(screen.queryByText('No cleanup runs yet.')).toBeNull()
  })

  describe('a failed REFETCH must not discard data (agent-os-wczm, agent-os-6iui)', () => {
    beforeEach(() => {
      mockGetCleanupHistory.mockResolvedValue({ runs: [SUCCESS_RUN], limit: 20 })
    })

    it('keeps the populated run-history table when a REFETCH fails', async () => {
      const { queryClient } = renderPanel()
      expect(await screen.findByText('scheduled')).toBeInTheDocument()

      mockGetCleanupHistory.mockRejectedValue(new Error('boom'))
      await queryClient.refetchQueries({ queryKey: ['settings', 'docker-cleanup-history'] })
      await waitFor(() =>
        expect(queryClient.getQueryState(['settings', 'docker-cleanup-history'])?.status).toBe('error'),
      )

      expect(screen.getByText('scheduled')).toBeInTheDocument()
      expect(screen.getByText('7.00 MB')).toBeInTheDocument()
      expect(screen.queryByText('Run history is unavailable.')).not.toBeInTheDocument()
      expect(screen.getByText(/Could not refresh the cleanup run history/)).toBeInTheDocument()
    })

    it('offers a Retry on the run-history notice that refetches the history', async () => {
      const { queryClient } = renderPanel()
      expect(await screen.findByText('scheduled')).toBeInTheDocument()

      mockGetCleanupHistory.mockRejectedValue(new Error('boom'))
      await queryClient.refetchQueries({ queryKey: ['settings', 'docker-cleanup-history'] })
      await waitFor(() =>
        expect(queryClient.getQueryState(['settings', 'docker-cleanup-history'])?.status).toBe('error'),
      )
      // Only the history query failed, so exactly one Retry is on screen.
      expect(screen.queryByText(/Could not refresh the cleanup schedule/)).not.toBeInTheDocument()

      const before = mockGetCleanupHistory.mock.calls.length
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
      await waitFor(() => expect(mockGetCleanupHistory.mock.calls.length).toBeGreaterThan(before))
    })

    it('keeps the panel and offers Retry when a policy REFETCH fails', async () => {
      const { queryClient } = renderPanel()
      expect(await screen.findByTestId('auto-cleanup-copy')).toBeInTheDocument()

      mockGetCleanupPolicy.mockRejectedValue(new Error('boom'))
      await queryClient.refetchQueries({ queryKey: ['settings', 'docker-cleanup'] })
      await waitFor(() =>
        expect(queryClient.getQueryState(['settings', 'docker-cleanup'])?.status).toBe('error'),
      )

      expect(screen.getByTestId('auto-cleanup-copy')).toBeInTheDocument()
      expect(screen.getByText(/Could not refresh the cleanup schedule/)).toBeInTheDocument()
    })
  })
})

// agent-os-1xo3: the endpoint is a real removal, so the tests below are about
// ORDER (confirm before the call), REFUSAL (cancel calls nothing), and what the
// operator is told afterwards.
describe('AutoCleanupPanel: Run now (agent-os-1xo3)', () => {
  async function clickRun() {
    fireEvent.click(await screen.findByRole('button', { name: 'Run now' }))
  }

  it('asks first, naming the stored categories and floor, and calls nothing until confirmed', async () => {
    mockGetCleanupPolicy.mockResolvedValue({ ...POLICY, categories: { ...CATEGORIES, containers: true } })
    renderPanel()
    await clickRun()

    expect(await screen.findByText('Run Docker cleanup now?')).toBeInTheDocument()
    expect(
      screen.getByText(/^This removes dangling images, build cache and stopped containers older than 168 h\./),
    ).toBeInTheDocument()
    expect(mockRunCleanup).not.toHaveBeenCalled()
  })

  it('calls nothing when the dialog is cancelled', async () => {
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(mockRunCleanup).not.toHaveBeenCalled()
    expect(screen.queryByText('Run Docker cleanup now?')).toBeNull()
  })

  it('runs once, at the stored floor the dialog named, when confirmed', async () => {
    mockGetCleanupPolicy.mockResolvedValue({ ...POLICY, minAgeHours: 72 })
    renderPanel()
    await clickRun()
    expect(await screen.findByText(/^This removes .* older than 72 h\./)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Run cleanup' }))

    await waitFor(() => expect(mockRunCleanup).toHaveBeenCalledTimes(1))
    expect(mockRunCleanup).toHaveBeenCalledWith(72)
  })

  it('is disabled while the run is in progress', async () => {
    mockRunCleanup.mockReturnValue(new Promise(() => {}))
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    const running = await screen.findByRole('button', { name: 'Running…' })
    expect(running).toBeDisabled()
    fireEvent.click(running)
    expect(mockRunCleanup).toHaveBeenCalledTimes(1)
  })

  it('refreshes history, images, dashboard stats, the cleanup readout, build cache and the host numbers', async () => {
    const { invalidate } = renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    await waitFor(() => expect(mockRunCleanup).toHaveBeenCalled())
    for (const key of [
      queryKeys.settings.dockerCleanupHistory(),
      queryKeys.resources.images(),
      queryKeys.dashboardStats(),
      queryKeys.resources.cleanupPreview(),
      queryKeys.resources.buildCache(),
      queryKeys.resources.volumes(),
      queryKeys.resources.networks(),
      queryKeys.resources.hostSummary(),
    ]) {
      await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: key }))
    }
  })

  it('tells the operator what the run removed', async () => {
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Cleanup finished: 3 images removed, 7.00 MB reclaimed'),
    )
  })

  it('shows a rejected run as a failure and never as a success', async () => {
    mockRunCleanup.mockRejectedValue(new Error('Docker cleanup failed'))
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.success).not.toHaveBeenCalled()
    // The button comes back so the operator can try again.
    expect(await screen.findByRole('button', { name: 'Run now' })).toBeEnabled()
  })

  it('shows the server refusal when another cleanup is already running (agent-os-yicu)', async () => {
    // The real backend body (handlers/docker_cleanup.go runCleanup): 409 CLEANUP_IN_PROGRESS.
    const refusal = 'A Docker cleanup is already running. Wait for it to finish, then check the history.'
    mockRunCleanup.mockRejectedValue({ code: 'CLEANUP_IN_PROGRESS', message: refusal, status: 409 })
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1))
    expect(toast.error).toHaveBeenCalledWith('Failed to run the cleanup', { description: refusal })
    expect(toast.success).not.toHaveBeenCalled()
    expect(await screen.findByRole('button', { name: 'Run now' })).toBeEnabled()
  })

  it('shows a failed run row as a failure when the server answered 200', async () => {
    mockRunCleanup.mockResolvedValue({ ...FAILED_RUN, minAgeHours: 168 })
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('shows a skipped run row as not done, with its reason', async () => {
    mockRunCleanup.mockResolvedValue({ ...SKIPPED_RUN, minAgeHours: 168 })
    renderPanel()
    await clickRun()
    fireEvent.click(await screen.findByRole('button', { name: 'Run cleanup' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Cleanup skipped, nothing was removed', {
        description: SKIPPED_RUN.errorMessage,
      }),
    )
    expect(toast.success).not.toHaveBeenCalled()
  })
})
