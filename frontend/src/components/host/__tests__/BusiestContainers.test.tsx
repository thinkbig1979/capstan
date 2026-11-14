import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ContainerMetric, ContainerMetricHistory } from '@/hooks/useMetricsBase'
import { BusiestContainers } from '../BusiestContainers'

const MiB = 1024 ** 2

function history(name: string, cpu: number): ContainerMetricHistory {
  const m: ContainerMetric = {
    cpuPercent: cpu,
    memUsage: 100 * MiB,
    memLimit: 1024 * MiB,
    memPercent: 10,
    netRx: 0,
    netTx: 0,
    blockRead: 0,
    blockWrite: 0,
    memSwap: 0,
    pids: 1,
  }
  return { containerId: `id-${name}`, name, metrics: [m] }
}

const CONTAINERS = [history('web', 50), history('db', 12.5)]

function ui(isConnected: boolean) {
  return (
    <MemoryRouter>
      <BusiestContainers containers={CONTAINERS} isConnected={isConnected} total={2} />
    </MemoryRouter>
  )
}

describe('BusiestContainers', () => {
  // Review round 1, item 1: useMetricsBase keeps the last containers when the
  // socket closes, so the figures must not stay up as if live.
  it('hides the last figures when the metrics socket drops', () => {
    const view = render(ui(true))
    expect(screen.getAllByTestId('busiest-container-row')).toHaveLength(2)
    expect(screen.getByText('50.0%')).toBeInTheDocument()

    view.rerender(ui(false))
    const list = screen.getByTestId('busiest-containers-list')
    expect(within(list).queryAllByTestId('busiest-container-row')).toHaveLength(0)
    expect(list).not.toHaveTextContent('%')
    expect(list).toHaveTextContent('Connecting to live metrics...')
  })

  // Review round 1, item 5: jsdom cannot measure, so this pins the classes the
  // reserve is computed from (5 x h-10 rows + 2 px border = 202 px).
  it('gives every row the fixed height the reserve is computed from', () => {
    render(ui(true))
    for (const row of screen.getAllByTestId('busiest-container-row')) {
      expect(row).toHaveClass('h-10')
      expect(row.className).not.toMatch(/\bpy-/)
    }
    expect(screen.getByTestId('busiest-containers-list')).toHaveClass('min-h-[202px]')
  })
})
