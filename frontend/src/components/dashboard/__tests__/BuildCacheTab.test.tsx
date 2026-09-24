import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BuildCacheTab } from '../BuildCacheTab'
import type { BuildCacheEntry } from '@/types'

// Typed on purpose: this fixture was an untyped literal in PascalCase, so tsc
// could not see it drift from the real payload (agent-os-iuby).
const mockEntries: BuildCacheEntry[] = [
  {
    id: 'abcdefghijklmno1234567',
    type: 'regular',
    description: 'layer cache',
    size: 1024000,
    shared: false,
    createdAt: '2026-04-28T00:00:00Z',
    lastUsedAt: '2026-04-29T00:00:00Z',
    usageCount: 5,
    inUse: true,
    parents: ['parent-1'],
  },
  {
    id: 'xyz1234567890abcdefgh',
    type: 'source.local',
    description: '',
    size: 512000,
    shared: false,
    createdAt: '2026-04-27T00:00:00Z',
    lastUsedAt: '2026-04-28T00:00:00Z',
    usageCount: 2,
    inUse: false,
  },
]

const { buildCache } = vi.hoisted(() => ({
  buildCache: {
    current: {} as { data: unknown; isLoading: boolean; isError: boolean; refetch: () => void },
  },
}))

vi.mock('@/hooks/useResources', () => ({
  useBuildCache: () => buildCache.current,
}))

beforeEach(() => {
  buildCache.current = { data: mockEntries, isLoading: false, isError: false, refetch: vi.fn() }
})

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    pruneBuildCache: vi.fn().mockResolvedValue({}),
  },
}))

vi.mock('@/components/dashboard/SortFilterBar', () => ({
  SortFilterBar: ({ countDisplay, actions }: { countDisplay: string; actions: React.ReactNode }) => (
    <div data-testid="sort-filter-bar">
      {countDisplay}
      {actions}
    </div>
  ),
}))

vi.mock('@/components/dashboard/PruneButton', () => ({
  PruneButton: ({ label }: { label: string }) => (
    <button data-testid="prune-button">{label}</button>
  ),
}))

describe('BuildCacheTab', () => {
  it('renders cache entries', () => {
    render(<BuildCacheTab />)
    expect(screen.getByText('layer cache')).toBeInTheDocument()
  })

  it('shows entry count and total size', () => {
    render(<BuildCacheTab />)
    expect(screen.getByTestId('sort-filter-bar').textContent).toContain('2 entries')
  })

  it('renders prune button', () => {
    render(<BuildCacheTab />)
    expect(screen.getByTestId('prune-button')).toBeInTheDocument()
  })

  it('shows In Use badge for active entries', () => {
    render(<BuildCacheTab />)
    expect(screen.getByText('Yes')).toBeInTheDocument()
  })
})

describe('BuildCacheTab — a failed Docker read is not an empty cache (agent-os-v824)', () => {
  it('first load fails: an error with Retry, NOT "No Build Cache"', () => {
    const refetch = vi.fn()
    buildCache.current = { data: undefined, isLoading: false, isError: true, refetch }
    render(<BuildCacheTab />)

    // Pre-fix this read "No Build Cache - Build cache is empty".
    expect(screen.queryByText('No Build Cache')).not.toBeInTheDocument()
    expect(screen.getByText('Could not load the build cache.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('a refetch fails over loaded entries: keeps them AND says so', () => {
    buildCache.current = { data: mockEntries, isLoading: false, isError: true, refetch: vi.fn() }
    render(<BuildCacheTab />)

    expect(screen.getByText('layer cache')).toBeInTheDocument()
    expect(
      screen.getByText('Could not refresh the build cache. The values shown are the last ones the server sent.'),
    ).toBeInTheDocument()
  })

  it('a genuinely empty cache still shows the empty state, with no error', () => {
    buildCache.current = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
    render(<BuildCacheTab />)

    expect(screen.getByText('No Build Cache')).toBeInTheDocument()
    expect(screen.queryByText(/Could not (load|refresh)/)).not.toBeInTheDocument()
  })
})
