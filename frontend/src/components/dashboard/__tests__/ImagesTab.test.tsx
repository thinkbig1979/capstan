import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    images: (...a: unknown[]) => mockGetImages(...a),
    deleteImage: (...a: unknown[]) => mockDeleteImage(...a),
    pruneImages: vi.fn().mockResolvedValue({}),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const image = (over: Partial<DockerImage> = {}): DockerImage => ({
  id: 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  repoTags: ['nginx:latest'],
  size: 1024 * 1024,
  created: 1754640000,
  containers: 0,
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

const renderTab = () => render(<ImagesTab />, { wrapper: createWrapper() })

beforeEach(() => {
  vi.clearAllMocks()
  mockGetImages.mockResolvedValue([image()])
  mockDeleteImage.mockResolvedValue({})
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
  it('asks before removing, and force-removes an image that is in use', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], containers: 2 }),
    ])
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Remove image nginx:latest' }))

    expect(await screen.findByText('Remove Image "nginx:latest"?')).toBeInTheDocument()
    expect(
      screen.getByText(/used by containers and will be force-removed/),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() =>
      expect(mockDeleteImage).toHaveBeenCalledWith('sha256:aaa', true),
    )
  })

  it('does not force-remove an unused image', async () => {
    mockGetImages.mockResolvedValue([
      image({ id: 'sha256:aaa', repoTags: ['nginx:latest'], containers: 0 }),
    ])
    renderTab()

    fireEvent.click(await screen.findByRole('button', { name: 'Remove image nginx:latest' }))
    expect(await screen.findByText(/This image will be removed/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(mockDeleteImage).toHaveBeenCalledWith('sha256:aaa', false))
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
