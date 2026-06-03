import { describe, it, expect, vi, beforeAll } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { DashboardMetricsTab } from '../DashboardMetricsTab'
import type { DashboardStats } from '@/types'
import type { DashboardAggregateMetrics } from '@/hooks/useDashboardMetrics'
import type { ContainerMetricHistory } from '@/hooks/useMetricsBase'

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: () => ({ width: 100, height: 32, top: 0, left: 0, bottom: 32, right: 100, x: 0, y: 0, toJSON: () => {} }),
  })
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
})

vi.stubGlobal('getComputedStyle', () => ({
  getPropertyValue: () => '#22c55e',
}))

function makeMetric(cpuPercent: number, memPercent: number) {
  return {
    cpuPercent,
    memUsage: memPercent * 1024 * 1024,
    memLimit: 100 * 1024 * 1024,
    memPercent,
    netRx: 0,
    netTx: 0,
    blockRead: 0,
    blockWrite: 0,
    memSwap: 0,
    pids: 1,
  }
}

const mockContainers: ContainerMetricHistory[] = [
  {
    containerId: 'c1',
    name: 'alpha',
    metrics: [makeMetric(10, 20)],
  },
  {
    containerId: 'c2',
    name: 'beta',
    metrics: [makeMetric(50, 5)],
  },
  {
    containerId: 'c3',
    name: 'gamma',
    metrics: [makeMetric(30, 80)],
  },
]

const mockStats: DashboardStats = {
  runningContainers: 3,
  totalContainers: 3,
  diskUsage: { total: 0, images: 0, containers: 0, volumes: 0, buildCache: 0 },
} as unknown as DashboardStats

const mockAggregates: DashboardAggregateMetrics = {
  totalCpuPercent: 90,
  totalMemUsage: 0,
  totalMemLimit: 0,
  totalMemPercent: 0,
  totalNetRx: 0,
  totalNetTx: 0,
  totalBlockRead: 0,
  totalBlockWrite: 0,
  totalSwap: 0,
  totalPids: 3,
}

function renderTab(containers = mockContainers) {
  return renderWithProviders(
    <DashboardMetricsTab
      stats={mockStats}
      aggregates={mockAggregates}
      isConnected={true}
      totalStacks={2}
      runningStacks={2}
      stoppedStacks={0}
      totalContainers={3}
      runningContainers={3}
      directoryCount={1}
      containers={containers}
    />,
  )
}

describe('ContainerSparklineList sorting', () => {
  it('renders all container rows', () => {
    renderTab()
    const rows = screen.getAllByTestId('container-sparkline-row')
    expect(rows).toHaveLength(3)
  })

  it('sorts by CPU descending by default — beta (50%) first, alpha (10%) last', () => {
    renderTab()
    const rows = screen.getAllByTestId('container-sparkline-row')
    const names = rows.map((r) => r.querySelector('.font-medium')?.textContent)
    expect(names[0]).toBe('beta')   // 50% CPU
    expect(names[1]).toBe('gamma')  // 30% CPU
    expect(names[2]).toBe('alpha')  // 10% CPU
  })

  it('toggles CPU sort to ascending when clicked again', () => {
    renderTab()
    const cpuBtn = screen.getByRole('button', { name: /cpu%/i })
    fireEvent.click(cpuBtn)

    const rows = screen.getAllByTestId('container-sparkline-row')
    const names = rows.map((r) => r.querySelector('.font-medium')?.textContent)
    expect(names[0]).toBe('alpha')  // 10% CPU — lowest first
    expect(names[2]).toBe('beta')   // 50% CPU — highest last
  })

  it('sorts by memory descending when Mem% button clicked', () => {
    renderTab()
    const memBtn = screen.getByRole('button', { name: /mem%/i })
    fireEvent.click(memBtn)

    const rows = screen.getAllByTestId('container-sparkline-row')
    const names = rows.map((r) => r.querySelector('.font-medium')?.textContent)
    expect(names[0]).toBe('gamma')  // 80% mem
    expect(names[1]).toBe('alpha')  // 20% mem
    expect(names[2]).toBe('beta')   // 5% mem
  })

  it('toggles memory sort to ascending when Mem% clicked twice', () => {
    renderTab()
    const memBtn = screen.getByRole('button', { name: /mem%/i })
    fireEvent.click(memBtn) // desc
    fireEvent.click(memBtn) // asc

    const rows = screen.getAllByTestId('container-sparkline-row')
    const names = rows.map((r) => r.querySelector('.font-medium')?.textContent)
    expect(names[0]).toBe('beta')   // 5% mem — lowest first
    expect(names[2]).toBe('gamma')  // 80% mem — highest last
  })

  it('shows a no-data message when containers list is empty', () => {
    renderTab([])
    expect(screen.getByText(/no live container metrics/i)).toBeInTheDocument()
  })

  it('shows container count label', () => {
    renderTab()
    expect(screen.getByText('3 containers')).toBeInTheDocument()
  })

  it('shows singular "container" for a single container', () => {
    renderTab([mockContainers[0]])
    expect(screen.getByText('1 container')).toBeInTheDocument()
  })
})
