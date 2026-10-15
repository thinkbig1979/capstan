import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { CategoryCard } from '../CategoryCard'
import { hostCards } from '../host-views'
import type { HostSummary } from '@/types/generated-services'

const GiB = 1024 ** 3

function summary(volumes: HostSummary['volumes']): HostSummary {
  return {
    disk: { total: GiB, inUse: GiB, reclaimable: 0, reclaimableAll: 0, volumesSizeKnown: volumes.sizeKnown },
    containers: { count: 0, running: 0, stopped: 0, reclaimableCount: 0, reclaimableBytes: 0 },
    images: { count: 0, inUse: 0, bytes: 0, reclaimableCount: 0, reclaimableBytes: 0 },
    volumes,
    networks: { count: 3, builtIn: 3, stackOwned: 0, reclaimableCount: 0 },
    buildCache: { count: 0, bytes: 0, reclaimableBytes: 0 },
    measuredAt: '2026-10-14T10:00:00Z',
  }
}

function renderVolumesCard(volumes: HostSummary['volumes']) {
  const model = hostCards(summary(volumes)).find((c) => c.category === 'volumes')!
  render(
    <MemoryRouter>
      <CategoryCard {...model} viewHref="/host/volumes" />
    </MemoryRouter>,
  )
  return screen.getByTestId('host-card-volumes')
}

describe('CategoryCard', () => {
  it('shows the reclaim line, View all and no Clean up action when something can be removed', () => {
    render(
      <MemoryRouter>
        <CategoryCard
          category="images"
          title="Images"
          value="21.00 GB"
          detail="48 images · 31 used by a container."
          reclaim="17 unused images · about 14.00 GB"
          viewHref="/host/images"
        />
      </MemoryRouter>,
    )
    const card = screen.getByTestId('host-card-images')
    expect(within(card).getByText('21.00 GB')).toBeInTheDocument()
    expect(screen.getByTestId('host-card-images-reclaim')).toHaveTextContent('17 unused images · about 14.00 GB')
    expect(within(card).queryByRole('link', { name: /^Clean up/ })).toBeNull()
    expect(within(card).queryByRole('button', { name: /^Clean up/ })).toBeNull()
    expect(within(card).getByRole('link', { name: /^View all/ })).toHaveAttribute('href', '/host/images')
    expect(within(card).queryByText('Nothing to clean up')).toBeNull()
  })

  it('shows "Nothing to clean up" when nothing can be removed', () => {
    render(
      <MemoryRouter>
        <CategoryCard
          category="networks"
          title="Networks"
          value="3"
          detail="0 used by stacks · 3 built in."
          viewHref="/host/networks"
        />
      </MemoryRouter>,
    )
    const card = screen.getByTestId('host-card-networks')
    expect(within(card).getByText('Nothing to clean up')).toBeInTheDocument()
    expect(within(card).queryByRole('link', { name: /^Clean up/ })).toBeNull()
    expect(screen.queryByTestId('host-card-networks-reclaim')).toBeNull()
  })

  // AC15: unknown is not zero.
  it('reads "size unknown" for volumes Docker could not size, never "0 B"', () => {
    const card = renderVolumesCard({
      count: 4, attached: 4, bytes: 0, sizeKnown: false, reclaimableCount: 0, reclaimableBytes: 0,
    })
    expect(within(card).getByText('size unknown')).toBeInTheDocument()
    expect(card).not.toHaveTextContent('0 B')
    expect(card).toHaveTextContent('4 volumes')
  })

  it('reads "0 B" next to the volume count when the size is a known 0', () => {
    const card = renderVolumesCard({
      count: 0, attached: 0, bytes: 0, sizeKnown: true, reclaimableCount: 0, reclaimableBytes: 0,
    })
    expect(within(card).getByText('0 B')).toBeInTheDocument()
    expect(card).toHaveTextContent('0 volumes')
    expect(card).not.toHaveTextContent('size unknown')
  })
})
