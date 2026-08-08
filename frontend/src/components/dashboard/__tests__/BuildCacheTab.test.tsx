import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
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

vi.mock('@/hooks/useResources', () => ({
  useBuildCache: () => ({
    data: mockEntries,
    isLoading: false,
  }),
}))

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
