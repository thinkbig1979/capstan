import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as React from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import type { HostCandidates } from '@/types/generated-services'
import type { DockerCleanupPolicy } from '@/types'

// agent-os-f9cf: the Free-up-space dialog (spec AC10, AC11).
const hostCandidates = vi.fn()
const freeSpace = vi.fn()
const getCleanupPolicy = vi.fn()
const updateCleanupPolicy = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    hostCandidates: (...a: unknown[]) => hostCandidates(...a),
    freeSpace: (...a: unknown[]) => freeSpace(...a),
    getCleanupPolicy: (...a: unknown[]) => getCleanupPolicy(...a),
    updateCleanupPolicy: (...a: unknown[]) => updateCleanupPolicy(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { toast } from 'sonner'
import { FreeUpSpaceDialog } from '../FreeUpSpaceDialog'

const MiB = 1024 ** 2

function makeCandidates(overrides: Partial<HostCandidates> = {}): HostCandidates {
  return {
    images: [
      { id: 'sha256:img1', label: 'postgres:15.4', size: 400 * MiB },
      { id: 'sha256:img2', label: 'node:20-alpine', size: 100 * MiB },
    ],
    buildCache: { count: 64, bytes: 200 * MiB },
    containers: [{ id: 'ctr1', label: 'adoring_curie', size: 10 * MiB, detail: 'Exited (0) 12 days ago' }],
    volumes: [{ id: 'old_pgdata', label: 'old_pgdata', size: 300 * MiB }],
    networks: [{ id: 'net1', label: 'demo_net', detail: 'bridge' }],
    ...overrides,
  }
}

function makePolicy(overrides: Partial<DockerCleanupPolicy> = {}): DockerCleanupPolicy {
  return {
    enabled: false,
    minAgeHours: 168,
    intervalHours: 24,
    categories: {
      danglingImages: true,
      unusedImages: false,
      buildCache: true,
      containers: false,
      volumes: false,
      networks: false,
    },
    alertThresholdBytes: 5 * 1024 ** 3,
    minAllowedAgeHours: 1,
    minAllowedIntervalHours: 1,
    ...overrides,
  }
}

function Harness() {
  const [open, setOpen] = React.useState(false)
  return (
    <>
      <button onClick={() => setOpen(true)}>Open it</button>
      <FreeUpSpaceDialog open={open} onOpenChange={setOpen} />
    </>
  )
}

async function openDialog() {
  const user = userEvent.setup()
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <Harness />
      </QueryClientProvider>
    </MemoryRouter>,
  )
  await user.click(screen.getByRole('button', { name: 'Open it' }))
  const dialog = await screen.findByRole('dialog', { name: 'Free up space' })
  await within(dialog).findByTestId('free-space-row-images')
  return { user, dialog }
}

const row = (id: string) => screen.getByTestId(`free-space-row-${id}`)
const box = (id: string) => within(row(id)).getAllByRole('checkbox')[0] as HTMLInputElement
const removeButton = () => screen.getByRole('button', { name: /^Remove selected$|^Removing/ })
const autoBox = () => screen.getByRole('checkbox', { name: 'Do this automatically' })

beforeEach(() => {
  vi.clearAllMocks()
  hostCandidates.mockResolvedValue(makeCandidates())
  getCleanupPolicy.mockResolvedValue(makePolicy())
  freeSpace.mockResolvedValue({ outcome: 'success', reason: 'removed 4 item(s)' })
  updateCleanupPolicy.mockResolvedValue(makePolicy({ enabled: true }))
})

describe('FreeUpSpaceDialog', () => {
  it('opens as a dialog named "Free up space" with images, cache and containers ticked, volumes and networks not', async () => {
    await openDialog()
    expect(box('images').checked).toBe(true)
    expect(box('buildCache').checked).toBe(true)
    expect(box('containers').checked).toBe(true)
    expect(box('volumes').checked).toBe(false)
    expect(box('networks').checked).toBe(false)
  })

  it('shows the volume data-loss warning on the Volumes row', async () => {
    await openDialog()
    expect(row('volumes')).toHaveTextContent(
      'Volumes can hold databases or uploads. Removal cannot be undone, so this is off by default. Back up first if unsure.',
    )
  })

  it('lists each row\'s items under "Show the <n> items", capped at 200 with "and <n> more"', async () => {
    const images = Array.from({ length: 205 }, (_, i) => ({ id: `sha256:i${i}`, label: `img-${i}`, size: MiB }))
    hostCandidates.mockResolvedValue(makeCandidates({ images }))
    await openDialog()
    const details = within(row('images')).getByText('Show the 205 items').closest('details')!
    expect(within(details).getAllByRole('listitem')).toHaveLength(201)
    expect(details).toHaveTextContent('img-199 · 1.00 MB')
    expect(details).not.toHaveTextContent('img-200')
    expect(details).toHaveTextContent('and 5 more')
    expect(within(row('containers')).getByText('Show the 1 items').closest('details')).toHaveTextContent(
      'adoring_curie · Exited (0) 12 days ago · 10.00 MB',
    )
    expect(within(row('buildCache')).getByText('Show the 64 items').closest('details')).toHaveTextContent('64 cache entries')
  })

  it('updates the selected total live as boxes change, and disables Remove selected when nothing is selected', async () => {
    const { user } = await openDialog()
    const total = screen.getByTestId('free-space-selected-total')
    // 500 MiB images + 200 MiB cache + 10 MiB containers.
    expect(total).toHaveTextContent('Selected: about 710.00 MB to reclaim')
    await user.click(box('images'))
    expect(total).toHaveTextContent('Selected: 210.00 MB to reclaim')
    await user.click(box('volumes'))
    expect(total).toHaveTextContent('Selected: 510.00 MB to reclaim')
    expect(removeButton()).toBeEnabled()

    await user.click(box('volumes'))
    await user.click(box('buildCache'))
    await user.click(box('containers'))
    expect(total).toHaveTextContent('Selected: 0 B to reclaim')
    expect(removeButton()).toBeDisabled()
  })

  it('sends only the ticked categories (no volumes key while Volumes is unticked) and shows a pending state', async () => {
    let resolve!: (v: unknown) => void
    freeSpace.mockReturnValue(new Promise((r) => (resolve = r)))
    const { user } = await openDialog()
    await user.click(removeButton())

    expect(freeSpace).toHaveBeenCalledTimes(1)
    const req = freeSpace.mock.calls[0][0]
    expect(req).toEqual({ images: ['sha256:img1', 'sha256:img2'], buildCache: true, containers: ['ctr1'] })
    expect(req).not.toHaveProperty('volumes')
    expect(req).not.toHaveProperty('networks')
    await waitFor(() => expect(removeButton()).toHaveTextContent('Removing...'))
    expect(removeButton()).toBeDisabled()

    resolve({ outcome: 'success', reason: 'removed 4 item(s)' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(toast.success).toHaveBeenCalledWith('removed 4 item(s)')
  })

  it('shows a partial result as a warning toast with the failed and skipped counts', async () => {
    freeSpace.mockResolvedValue({ outcome: 'partial', reason: 'removed 2 item(s); 1 failed, 1 skipped' })
    const { user } = await openDialog()
    await user.click(removeButton())
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('removed 2 item(s); 1 failed, 1 skipped'))
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('closes on Escape and on Cancel, returning focus to the opener', async () => {
    const { user } = await openDialog()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open it' })).toHaveFocus())

    await user.click(screen.getByRole('button', { name: 'Open it' }))
    const dialog = await screen.findByRole('dialog', { name: 'Free up space' })
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open it' })).toHaveFocus())
    expect(freeSpace).not.toHaveBeenCalled()
  })

  it('says which stacks were withheld when a compose file could not be read', async () => {
    hostCandidates.mockResolvedValue(
      makeCandidates({ images: [], containers: [], volumes: [], networks: [], withheldStacks: ['blog', 'shop'] }),
    )
    await openDialog()
    expect(screen.getByTestId('free-space-withheld')).toHaveTextContent(
      'Capstan could not read the compose file of blog and shop. Until it can, only build cache is listed',
    )
  })

  it('shows no withheld notice when every compose file was read', async () => {
    await openDialog()
    expect(screen.queryByTestId('free-space-withheld')).toBeNull()
  })
})

describe('FreeUpSpaceDialog "Do this automatically"', () => {
  it('PUTs only true categories after the removal resolves, mapping Unused images to both image categories', async () => {
    const order: string[] = []
    freeSpace.mockImplementation(async () => {
      order.push('free')
      return { outcome: 'success', reason: 'removed 3 item(s)' }
    })
    updateCleanupPolicy.mockImplementation(async () => {
      order.push('put')
      return makePolicy({ enabled: true })
    })
    const { user } = await openDialog()
    await user.click(box('buildCache'))
    await user.click(autoBox())
    await user.click(removeButton())

    await waitFor(() => expect(updateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(order).toEqual(['free', 'put'])
    const body = updateCleanupPolicy.mock.calls[0][0]
    expect(body).toEqual({ enabled: true, categories: { danglingImages: true, unusedImages: true, containers: true } })
    expect(Object.values(body.categories)).not.toContain(false)
    expect(body.categories).not.toHaveProperty('volumes')
  })

  it('does not PUT when the box is left unchecked, or when the removal is rejected', async () => {
    const { user } = await openDialog()
    await user.click(removeButton())
    await waitFor(() => expect(freeSpace).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(updateCleanupPolicy).not.toHaveBeenCalled()

    freeSpace.mockRejectedValueOnce({ outcome: 'failed', reason: 'Docker is not answering', status: 503 })
    await user.click(screen.getByRole('button', { name: 'Open it' }))
    await screen.findByTestId('free-space-row-images')
    await user.click(autoBox())
    await user.click(removeButton())
    await waitFor(() => expect(freeSpace).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(updateCleanupPolicy).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('with Volumes ticked, warns inline and asks first: going back sends nothing, confirming sends volumes true', async () => {
    const { user } = await openDialog()
    await user.click(box('volumes'))
    await user.click(autoBox())
    expect(screen.getByTestId('free-space-auto-volume-warning')).toHaveTextContent(
      'Volumes can hold databases or uploads. Automatic removal cannot be undone.',
    )

    await user.click(removeButton())
    const confirm = screen.getByTestId('free-space-volume-confirm')
    expect(confirm).toHaveTextContent('unattached volumes, every 24 h')
    await user.click(within(confirm).getByRole('button', { name: 'Go back' }))
    expect(screen.queryByTestId('free-space-volume-confirm')).toBeNull()
    expect(freeSpace).not.toHaveBeenCalled()
    expect(updateCleanupPolicy).not.toHaveBeenCalled()

    await user.click(removeButton())
    await user.click(within(screen.getByTestId('free-space-volume-confirm')).getByRole('button', { name: 'Remove and turn on' }))
    await waitFor(() => expect(updateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(freeSpace.mock.calls[0][0].volumes).toEqual(['old_pgdata'])
    expect(updateCleanupPolicy.mock.calls[0][0].categories).toMatchObject({ volumes: true })
  })

  it('asks no volume question when Volumes is unticked', async () => {
    const { user } = await openDialog()
    await user.click(autoBox())
    expect(screen.queryByTestId('free-space-auto-volume-warning')).toBeNull()
    await user.click(removeButton())
    expect(screen.queryByTestId('free-space-volume-confirm')).toBeNull()
    await waitFor(() => expect(updateCleanupPolicy).toHaveBeenCalledTimes(1))
  })

  it('names the full effective set when the policy is off, including categories already stored as on', async () => {
    const { user } = await openDialog()
    await user.click(box('buildCache'))
    // Stored on: dangling images, build cache. Ticked: images (both kinds), containers.
    expect(screen.getByTestId('free-space-auto-copy')).toHaveTextContent(
      'Automatically remove dangling images, unused tagged images, build cache and stopped containers older than 168 h, every 24 h.',
    )
  })

  it('names only the added categories when the policy is already on', async () => {
    getCleanupPolicy.mockResolvedValue(makePolicy({ enabled: true, intervalHours: 12 }))
    await openDialog()
    expect(await screen.findByTestId('free-space-auto-copy')).toHaveTextContent(
      'Also remove unused tagged images and stopped containers older than 168 h, every 12 h.',
    )
  })

  it('says "already covers these" with a Change timing link when the enabled policy covers every ticked category', async () => {
    getCleanupPolicy.mockResolvedValue(
      makePolicy({
        enabled: true,
        categories: { danglingImages: true, unusedImages: true, buildCache: true, containers: true, volumes: false, networks: false },
      }),
    )
    await openDialog()
    const covered = await screen.findByTestId('free-space-auto-covered')
    expect(covered).toHaveTextContent('Automatic cleanup already covers these')
    expect(within(covered).getByRole('link', { name: 'Change timing' })).toHaveAttribute('href', '/host#automatic-cleanup')
    expect(screen.queryByRole('checkbox', { name: 'Do this automatically' })).toBeNull()
  })

  it('offers no automatic option while the policy cannot be read', async () => {
    getCleanupPolicy.mockRejectedValue(new Error('boom'))
    await openDialog()
    await waitFor(() => expect(getCleanupPolicy).toHaveBeenCalled())
    expect(screen.queryByRole('checkbox', { name: 'Do this automatically' })).toBeNull()
    expect(screen.queryByTestId('free-space-auto-covered')).toBeNull()
  })
})
