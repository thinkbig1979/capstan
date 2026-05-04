import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BuildCacheTab } from '../BuildCacheTab'

const mockEntries = [
  {
    ID: 'abcdefghijklmno1234567',
    Type: 'regular',
    Description: 'layer cache',
    Size: 1024000,
    LastUsedAt: '2026-04-29T00:00:00Z',
    UsageCount: 5,
    InUse: true,
  },
  {
    ID: 'xyz1234567890abcdefgh',
    Type: 'source.local',
    Description: '',
    Size: 512000,
    LastUsedAt: '2026-04-28T00:00:00Z',
    UsageCount: 2,
    InUse: false,
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
