import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import type { ReactNode } from 'react'
import { ImagesTab } from '../ImagesTab'
import type { DockerImage } from '@/types'

/**
 * DashboardPage.test.tsx:89 replaces this tab with an empty div, and Radix Tabs
 * unmount inactive content so Playwright never renders it either (0/43
 * statements, agent-os-m1mu). Rendered for real here with the API layer mocked,
 * so useImages, useDeleteImage and the invalidation all run.
 */

const mockGetImages = vi.fn()
const mockDeleteImage = vi.fn()
const mockPreviewCleanup = vi.fn()
const mockGetCleanupPolicy = vi.fn()
const mockPruneImages = vi.fn()
const mockImagePruneImpact = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    images: (...a: unknown[]) => mockGetImages(...a),
    deleteImage: (...a: unknown[]) => mockDeleteImage(...a),
    pruneImages: (...a: unknown[]) => mockPruneImages(...a),
    imagePruneImpact: (...a: unknown[]) => mockImagePruneImpact(...a),
    previewCleanup: (...a: unknown[]) => mockPreviewCleanup(...a),
    getCleanupPolicy: (...a: unknown[]) => mockGetCleanupPolicy(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// Both cleanup queries resolve by default. Without them every test below takes
// the no-data branch and passes whatever the readout does — a green that proves
// nothing. The empty preview is the quiet case: it renders a sentence, so the
// readout is on screen in all the pre-existing tests too.
const POLICY = {
  enabled: false,
  minAgeHours: 168,
  intervalHours: 24,
  minAllowedAgeHours: 1,
  minAllowedIntervalHours: 1,
}
// 72, deliberately NOT the policy's 168. The readout must name the floor the
// PREVIEW was computed at; with both fixtures on the same number, an
// implementation reading the policy (or hard-coding 168) passes and the test
// has measured nothing.
const EMPTY_PREVIEW = { candidates: [], reclaimableBytes: 0, cacheReclaimableBytes: 0, minAgeHours: 72 }

const image = (over: Partial<DockerImage> = {}): DockerImage => ({
  id: 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  repoTags: ['nginx:latest'],
  size: 1024 * 1024,
  sharedSize: 0,
  created: 1754640000,
  containers: 0,
  containersKnown: true,
  ...over,
})

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

// ImagesTab links to the cleanup settings pane, and a react-router Link throws
// outside a Router — the same wrapper StacksTab.test.tsx:161 uses.
const renderTab = () =>
  render(
    <MemoryRouter>
      <ImagesTab />
    </MemoryRouter>,
    { wrapper: createWrapper() },
  )

beforeEach(() => {
  vi.clearAllMocks()
  mockGetImages.mockResolvedValue([image()])
  mockDeleteImage.mockResolvedValue({})
  mockPreviewCleanup.mockResolvedValue(EMPTY_PREVIEW)
  mockGetCleanupPolicy.mockResolvedValue(POLICY)
  mockPruneImages.mockResolvedValue({ outcome: 'success', details: { imagesDeleted: 1 } })
  mockImagePruneImpact.mockResolvedValue({ stacks: [], uncheckedStacks: [] })
})

describe('ImagesTab — the scheduled-cleanup readout', () => {
  it('reports what a scheduled run would reclaim at the stored age floor', async () => {
    mockPreviewCleanup.mockResolvedValue({
      candidates: [
        { id: 'sha256:1111111111111111', size: 2 * 1024 * 1024 * 1024, created: 1750000000 },
        { id: 'sha256:2222222222222222', size: 1024 * 1024 * 1024, created: 1750000001 },
      ],
      reclaimableBytes: 3221225472,
      cacheReclaimableBytes: 0,
      minAgeHours: 72,
    })
    renderTab()

    // Queried by test id, not by text: the sentence spans a <p> and the elements
    // inside it, so getByText would match more than one element and throw.
    const readout = await screen.findByTestId('cleanup-reclaimable')
    expect(readout).toHaveTextContent('3.00 GB')
    expect(readout).toHaveTextContent('2 dangling images')
    // 72 is the preview's floor; the policy fixture says 168. Only a readout
    // reading the response can produce this.
    expect(readout).toHaveTextContent('created more than 72 hours ago')
    expect(readout).not.toHaveTextContent('168')
    // The floor comes off the response, not out of the policy query.
    await waitFor(() => expect(mockPreviewCleanup).toHaveBeenCalledWith())
  })

  it('adds the unused build cache a run would also prune (agent-os-nacp)', async () => {
    mockPreviewCleanup.mockResolvedValue({
      candidates: [{ id: 'sha256:1111111111111111', size: 1024 * 1024 * 1024, created: 1750000000 }],
      reclaimableBytes: 1024 * 1024 * 1024,
      cacheReclaimableBytes: 2 * 1024 * 1024 * 1024,
      minAgeHours: 72,
    })
    renderTab()

    const readout = await screen.findByTestId('cleanup-reclaimable')
    expect(readout).toHaveTextContent(
      'Scheduled cleanup would reclaim 1.00 GB from 1 dangling image and 2.00 GB of unused build cache, all older than 72 hours.',
    )
  })

  it('reports cache-only reclaim instead of saying there is nothing to reclaim', async () => {
    mockPreviewCleanup.mockResolvedValue({
      candidates: [],
      reclaimableBytes: 0,
      cacheReclaimableBytes: 2 * 1024 * 1024 * 1024,
      minAgeHours: 72,
    })
    renderTab()

    const readout = await screen.findByTestId('cleanup-reclaimable')
    expect(readout).toHaveTextContent(
      'No dangling image is older than 72 hours. Scheduled cleanup would reclaim 2.00 GB of unused build cache.',
    )
    expect(readout).not.toHaveTextContent('Nothing for scheduled cleanup to reclaim')
  })

  it('names the schedule state and links to the automatic cleanup panel', async () => {
    mockGetCleanupPolicy.mockResolvedValue({ ...POLICY, enabled: true })
    renderTab()

    const readout = await screen.findByTestId('cleanup-reclaimable')
    await waitFor(() => expect(readout).toHaveTextContent('Scheduled cleanup is on.'))
    expect(screen.getByRole('link', { name: 'Automatic cleanup' })).toHaveAttribute(
      'href',
      '/host#automatic-cleanup',
    )
  })

  it('says there is nothing to reclaim rather than showing a fabricated 0 B', async () => {
    renderTab()

    const readout = await screen.findByTestId('cleanup-reclaimable')
    expect(readout).toHaveTextContent(
      'Nothing for scheduled cleanup to reclaim: no dangling image was created more than 72 hours ago.',
    )
    expect(readout).not.toHaveTextContent('0 B')
  })

  it('renders no readout at all when the preview fails, even with a readable policy', async () => {
    // A preview that fails while the image list is healthy — a transient error
    // on the preview call alone. (A Docker-less host is NOT this case: the
    // images query fails too and the tab early-returns to its load-failed
    // notice before the readout is ever evaluated, agent-os-v824.) The whole block goes,
    // schedule sentence and link included: a schedule line with no figure
    // beside it reads as "nothing to reclaim".
    mockPreviewCleanup.mockRejectedValue(new Error('docker unavailable'))
    renderTab()

    // The image list is unaffected: a failed preview is not a failed list.
    expect(await screen.findByText('nginx:latest')).toBeInTheDocument()
    await waitFor(() => expect(mockPreviewCleanup).toHaveBeenCalled())
    await waitFor(() => expect(mockGetCleanupPolicy).toHaveBeenCalled())

    expect(screen.queryByTestId('cleanup-reclaimable')).toBeNull()
    expect(screen.queryByText(/Scheduled cleanup/)).toBeNull()
    expect(screen.queryByRole('link', { name: 'Automatic cleanup' })).toBeNull()
  })
})

describe('ImagesTab — loading and empty', () => {
  it('shows skeletons while loading', () => {
    mockGetImages.mockReturnValue(new Promise(() => {}))
    renderTab()

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByText('No Images')).not.toBeInTheDocument()
  })

  it('shows the empty state when the host has no images', async () => {
    mockGetImages.mockResolvedValue([])
    renderTab()

    expect(await screen.findByText('No Images')).toBeInTheDocument()
    expect(screen.getByText('No Docker images found on this host')).toBeInTheDocument()
  })
})

describe('ImagesTab — the table', () => {
  it('renders each repo tag as its own badge', async () => {
    mockGetImages.mockResolvedValue([image({ repoTags: ['nginx:latest', 'nginx:1.27'] })])
    renderTab()

    expect(await screen.findByText('nginx:latest')).toBeInTheDocument()
    expect(screen.getByText('nginx:1.27')).toBeInTheDocument()
  })

  it('marks an untagged image as <none>:<none>', async () => {
    mockGetImages.mockResolvedValue([image({ repoTags: [] })])
    renderTab()

    expect(await screen.findByTitle('Untagged (dangling) image')).toHaveTextContent(
      '<none>:<none>',
    )
  })

  it('strips the sha256: prefix and truncates the id', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:0123456789abcdef0123456789abcdef' }),
    ])
    renderTab()

    expect(await screen.findByText('0123456789abcdef012')).toBeInTheDocument()
  })

  it('shows a container count badge only when the image is in use', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:a', repoTags: ['used'], containers: 2 }),
      image({ id: 'sha256:b', repoTags: ['unused'], containers: 0 }),
    ])
    renderTab()

    expect(await screen.findByText('2')).toBeInTheDocument()
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  // agent-os-qczn: the server could not count (its DiskUsage read failed). A
  // "0" here would read as "unused, safe to remove".
  it('shows "unknown", not 0, when the server could not count containers', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:a', repoTags: ['mystery'], containers: 0, containersKnown: false }),
    ])
    renderTab()

    const row = (await screen.findByText('mystery')).closest('tr') as HTMLElement
    expect(row).toHaveTextContent('unknown')
    expect(within(row).queryByText('0')).not.toBeInTheDocument()
  })

  it('summarises the count and total size in the header', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:a', size: 1024 * 1024 }),
      image({ id: 'sha256:b', repoTags: ['redis:7'], size: 1024 * 1024 }),
    ])
    renderTab()

    expect(await screen.findByText(/^2 images, /)).toBeInTheDocument()
  })
})

describe('ImagesTab — filtering and sorting', () => {
  const TWO = [
    image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], size: 100 }),
    image({ id: 'sha256:bbb', repoTags: ['redis:7'], size: 900 }),
  ]

  it('filters on the repo tags', async () => {
    mockGetImages.mockResolvedValue(TWO)
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter images…'), {
      target: { value: 'redis' },
    })

    expect(screen.getByText('redis:7')).toBeInTheDocument()
    expect(screen.queryByText('nginx:latest')).not.toBeInTheDocument()
  })

  it('filters on the image id as well', async () => {
    mockGetImages.mockResolvedValue(TWO)
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter images…'), {
      target: { value: 'bbb' },
    })

    expect(screen.getByText('redis:7')).toBeInTheDocument()
    expect(screen.queryByText('nginx:latest')).not.toBeInTheDocument()
  })

  it('switches the count display while filtering', async () => {
    mockGetImages.mockResolvedValue(TWO)
    renderTab()

    fireEvent.change(await screen.findByPlaceholderText('Filter images…'), {
      target: { value: 'redis' },
    })

    expect(screen.getByText('1 of 2 images')).toBeInTheDocument()
  })

  it('sorts by size largest-first by default', async () => {
    mockGetImages.mockResolvedValue(TWO)
    renderTab()

    await screen.findByText('redis:7')
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows[0]).toHaveTextContent('redis:7')
  })

  it('re-sorts by name when Name is chosen', async () => {
    mockGetImages.mockResolvedValue(TWO)
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Name' }))

    const rows = screen.getAllByRole('row').slice(1)
    expect(rows[0]).toHaveTextContent('nginx:latest')
  })
})

describe('ImagesTab — deleting', () => {
  // agent-os-qczn: once counts are real, the old "force-remove an in-use
  // image" path would have force-removed every image a container runs from.
  it('disables Delete for an image in use, and says why in a tooltip', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], containers: 2 }),
    ])
    renderTab()

    const button = await screen.findByRole('button', {
      name: 'Remove image nginx:latest (used by 2 containers, cannot be removed)',
    })
    expect(button).toBeDisabled()

    fireEvent.focus(button.parentElement as HTMLElement)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Used by 2 containers')

    fireEvent.click(button)
    expect(screen.queryByText('Remove Image "nginx:latest"?')).not.toBeInTheDocument()
    expect(mockDeleteImage).not.toHaveBeenCalled()
  })

  it('says "1 container", not "1 containers"', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], containers: 1 }),
    ])
    renderTab()

    const button = await screen.findByRole('button', {
      name: 'Remove image nginx:latest (used by 1 container, cannot be removed)',
    })
    fireEvent.focus(button.parentElement as HTMLElement)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Used by 1 container')
  })

  it('never forces: an image with an unknown count is removed with force false', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], containers: 0, containersKnown: false }),
    ])
    renderTab()

    const button = await screen.findByRole('button', { name: 'Remove image nginx:latest' })
    expect(button).toBeEnabled()
    fireEvent.click(button)
    expect(await screen.findByText('Remove Image "nginx:latest"?')).toBeInTheDocument()
    expect(screen.queryByText(/force-removed/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(mockDeleteImage).toHaveBeenCalledTimes(1))
    // Every call, not "some call": toHaveBeenCalledWith matches any one of them.
    expect(mockDeleteImage.mock.calls.map((c) => c[1])).toEqual([false])
  })

  it('does not force-remove an unused image', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], containers: 0 }),
    ])
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Remove image nginx:latest' }))
    expect(await screen.findByText(/This image will be removed/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(mockDeleteImage).toHaveBeenCalledTimes(1))
    expect(mockDeleteImage.mock.calls.map((c) => c[1])).toEqual([false])
  })

  it('deletes nothing when the confirmation is dismissed', async () => {
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: /Remove image/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() =>
      expect(screen.queryByText(/Remove Image/)).not.toBeInTheDocument(),
    )
    expect(mockDeleteImage).not.toHaveBeenCalled()
  })

  it('names an untagged image by its id in the confirmation', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:0123456789abcdef0123', repoTags: [] }),
    ])
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: /Remove image/ }))

    // id.substring(0, 19) — 'sha256:' plus 12 hex characters.
    expect(await screen.findByText('Remove Image "sha256:0123456789ab"?')).toBeInTheDocument()
  })
})

describe('ImagesTab — a failed Docker read is not an empty host (agent-os-v824)', () => {
  const serverFault = { status: 500, code: 'DOCKER_ERROR', message: 'docker unavailable' }

  function renderWithClient() {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
    })
    render(<MemoryRouter><ImagesTab /></MemoryRouter>, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    })
    return queryClient
  }

  it('first load fails: an error with Retry, NOT "No Images"', async () => {
    mockGetImages.mockRejectedValue(serverFault)
    const queryClient = renderWithClient()
    await waitFor(() =>
      expect(queryClient.getQueryState(['resources', 'images'])?.status).toBe('error'),
    )

    // Pre-fix this read "No Images": a failed Docker read shown as fact.
    expect(screen.queryByText('No Images')).not.toBeInTheDocument()
    expect(screen.getByText('Could not load the image list.')).toBeInTheDocument()

    mockGetImages.mockResolvedValue([image()])
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('nginx:latest')).toBeInTheDocument()
    expect(screen.queryByText('Could not load the image list.')).not.toBeInTheDocument()
  })

  it('a refetch fails over a loaded list: keeps the list AND says so', async () => {
    const queryClient = renderWithClient()
    expect(await screen.findByText('nginx:latest')).toBeInTheDocument()

    mockGetImages.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(queryClient.getQueryState(['resources', 'images'])?.status).toBe('error'),
    )

    // Retention first, so a red below means RETAINED AND UNDISCLOSED.
    expect(screen.getByText('nginx:latest')).toBeInTheDocument()
    expect(
      screen.getByText('Could not refresh the image list. The values shown are the last ones the server sent.'),
    ).toBeInTheDocument()
  })

  it('a host that really has none still shows the empty state, with no error', async () => {
    mockGetImages.mockResolvedValue([])
    renderWithClient()

    expect(await screen.findByText('No Images')).toBeInTheDocument()
    expect(screen.queryByText(/Could not (load|refresh)/)).not.toBeInTheDocument()
  })
})

// agent-os-tt94: "all unused" removes every image no container uses, a
// stopped or removed stack's images included. The dialog names those stacks
// and asks for the word "prune" before it removes anything.
describe('ImagesTab — prune warns about stacks that would lose images', () => {
  async function openPruneWithAll(all: boolean) {
    const user = userEvent.setup()
    renderTab()
    await screen.findByText('nginx:latest')
    await user.click(screen.getByRole('button', { name: /^prune$/i }))
    await screen.findByText('Prune Unused Images?')
    if (all) await user.click(screen.getByRole('switch'))
    return user
  }
  const confirmButton = () => screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
  const typedBox = () => screen.queryByRole('textbox', { name: /type prune to confirm/i })

  it('names the affected stack and keeps Prune disabled until "prune" is typed', async () => {
    mockImagePruneImpact.mockResolvedValue({
      stacks: [{ stack: 'shop', items: ['postgres:16', 'shop-api:latest'] }],
      uncheckedStacks: [],
    })
    const user = await openPruneWithAll(true)

    expect(await screen.findByText(/stopped or removed stacks would lose these images/i)).toBeInTheDocument()
    expect(screen.getByText('shop')).toBeInTheDocument()
    expect(screen.getByText(/postgres:16, shop-api:latest/)).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()

    await user.type(typedBox()!, 'prune')
    expect(confirmButton()).toBeEnabled()
    await user.click(confirmButton())
    await waitFor(() => expect(mockPruneImages).toHaveBeenCalledTimes(1))
    expect(mockPruneImages.mock.calls[0][0]).toEqual({ all: true, until: undefined })
  })

  it('says it could not check, and still requires typing, when the check fails', async () => {
    mockImagePruneImpact.mockRejectedValue(new Error('500'))
    await openPruneWithAll(true)

    expect(await screen.findByText(/could not check which stacks this prune affects/i)).toBeInTheDocument()
    expect(typedBox()).toBeInTheDocument()
    expect(confirmButton()).toBeDisabled()
  })

  it('does not check or ask for typing when only dangling images are pruned', async () => {
    await openPruneWithAll(false)

    expect(typedBox()).not.toBeInTheDocument()
    expect(confirmButton()).toBeEnabled()
    expect(mockImagePruneImpact).not.toHaveBeenCalled()
  })
})
