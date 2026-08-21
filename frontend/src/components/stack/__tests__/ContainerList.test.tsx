import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { ContainerList } from '../ContainerList'
import type { Container } from '@/types'
import type { ContainerMetric } from '@/hooks/useMetricsBase'

const restartContainer = vi.fn()
vi.mock('@/lib/api', () => ({
  resourcesApi: {
    restartContainer: (...args: unknown[]) => restartContainer(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

const baseContainer: Container = {
  id: 'container-1',
  name: 'my-container',
  image: 'nginx:latest',
  state: 'running',
  status: 'Up 2 hours',
  ports: [],
}

function makeContainer(overrides: Partial<Container> = {}): Container {
  return { ...baseContainer, ...overrides }
}

function makeMetric(overrides: Partial<ContainerMetric> = {}): ContainerMetric {
  return {
    cpuPercent: 12.3,
    memUsage: 512 * 1024 * 1024,
    memLimit: 1024 * 1024 * 1024,
    memPercent: 50,
    netRx: 0,
    netTx: 0,
    blockRead: 0,
    blockWrite: 0,
    memSwap: 0,
    pids: 3,
    ...overrides,
  }
}

function renderList(props: Partial<Parameters<typeof ContainerList>[0]> = {}) {
  return renderWithProviders(
    <ContainerList containers={[makeContainer()]} stackId="stack-1" {...props} />,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  restartContainer.mockResolvedValue({ message: 'Container restarted' })
})

describe('ContainerList', () => {
  it('shows "Stack is stopped" message when containers array is empty', () => {
    renderList({ containers: [] })

    expect(screen.getByText(/Stack is stopped/)).toBeInTheDocument()
  })

  it('shows "Stack is stopped" message when containers is undefined', () => {
    renderList({ containers: undefined as unknown as Container[] })

    expect(screen.getByText(/Stack is stopped/)).toBeInTheDocument()
  })

  it('renders a table row for each container in desktop view', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
      makeContainer({ id: 'c2', name: 'db' }),
      makeContainer({ id: 'c3', name: 'cache' }),
    ]

    renderList({ containers })

    expect(screen.getAllByText('web').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('db').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('cache').length).toBeGreaterThanOrEqual(1)
  })

  it('renders a card for each container in mobile view', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
      makeContainer({ id: 'c2', name: 'api' }),
    ]

    renderList({ containers })

    const names = screen.getAllByText('web')
    expect(names.length).toBeGreaterThanOrEqual(2)

    const apiNames = screen.getAllByText('api')
    expect(apiNames.length).toBeGreaterThanOrEqual(2)
  })

  describe('ports', () => {
    it('renders a tcp port as a chip linking to the published host port', () => {
      const containers = [
        makeContainer({
          id: 'c1',
          name: 'web',
          ports: [{ host: '0.0.0.0:8080', container: '80/tcp', protocol: 'tcp' }],
        }),
      ]

      renderList({ containers })

      const chips = screen.getAllByRole('link', { name: '8080→80' })
      expect(chips.length).toBeGreaterThanOrEqual(1)
      // A 0.0.0.0 bind is unreachable as a link target; the page's own host is.
      expect(chips[0]).toHaveAttribute('href', `http://${window.location.hostname}:8080`)
    })

    it('links a specific bind IP directly', () => {
      const containers = [
        makeContainer({
          id: 'c1',
          name: 'web',
          ports: [{ host: '192.168.1.5:8443', container: '443/tcp', protocol: 'tcp' }],
        }),
      ]

      renderList({ containers })

      const chips = screen.getAllByRole('link', { name: '8443→443' })
      expect(chips[0]).toHaveAttribute('href', 'http://192.168.1.5:8443')
    })

    it('renders a udp port as a plain, unlinked chip with its protocol', () => {
      const containers = [
        makeContainer({
          id: 'c1',
          name: 'dns',
          ports: [{ host: '0.0.0.0:53', container: '53/udp', protocol: 'udp' }],
        }),
      ]

      renderList({ containers })

      expect(screen.getAllByText('53→53/udp').length).toBeGreaterThanOrEqual(1)
      expect(screen.queryByRole('link', { name: /53/ })).not.toBeInTheDocument()
    })

    it('collapses a dual-stack (IPv4+IPv6) publish into one chip', () => {
      const containers = [
        makeContainer({
          id: 'c1',
          name: 'web',
          ports: [
            { host: '0.0.0.0:8085', container: '80/tcp', protocol: 'tcp' },
            { host: ':::8085', container: '80/tcp', protocol: 'tcp' },
          ],
        }),
      ]

      renderList({ containers })

      // One chip in the desktop table, one in the mobile card — not two each.
      expect(screen.getAllByRole('link', { name: '8085→80' })).toHaveLength(2)
    })

    it('displays a dash when no ports exist', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', ports: [] })] })

      expect(screen.getAllByText('—').length).toBeGreaterThan(0)
    })

    it('displays multiple ports', () => {
      const containers = [
        makeContainer({
          id: 'c1',
          name: 'web',
          ports: [
            { host: '0.0.0.0:80', container: '80/tcp', protocol: 'tcp' },
            { host: '0.0.0.0:443', container: '443/tcp', protocol: 'tcp' },
          ],
        }),
      ]

      renderList({ containers })

      expect(screen.getAllByRole('link', { name: '80→80' }).length).toBeGreaterThanOrEqual(1)
      expect(screen.getAllByRole('link', { name: '443→443' }).length).toBeGreaterThanOrEqual(1)
    })
  })

  describe('state and health', () => {
    it('renders running state label', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', state: 'running' })] })

      expect(screen.getAllByText('running').length).toBeGreaterThan(0)
    })

    it.each(['exited', 'dead', 'restarting'] as const)('renders %s state label', (state) => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', state })] })

      expect(screen.getAllByText(state).length).toBeGreaterThan(0)
    })

    it('renders healthy badge when health is healthy', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', health: 'healthy' })] })

      expect(screen.getAllByText('Healthy').length).toBeGreaterThan(0)
    })

    it('renders unhealthy badge when health is unhealthy', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', health: 'unhealthy' })] })

      expect(screen.getAllByText('Unhealthy').length).toBeGreaterThan(0)
    })

    it('renders none badge when health is undefined', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web' })] })

      expect(screen.getAllByText('none').length).toBeGreaterThan(0)
    })

    it('renders custom health status as outline badge', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', health: 'starting' })] })

      expect(screen.getAllByText('starting').length).toBeGreaterThan(0)
    })

    it('keeps the docker status text visible in the mobile card', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', status: 'Up 5 minutes' })] })

      expect(screen.getAllByText('Up 5 minutes').length).toBeGreaterThanOrEqual(1)
    })
  })

  describe('image cell', () => {
    it('emphasizes the tag separately from the repo', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web', image: 'nginx:1.25' })] })

      expect(screen.getAllByText('nginx:').length).toBeGreaterThanOrEqual(2)
      expect(screen.getAllByText('1.25').length).toBeGreaterThanOrEqual(2)
    })

    it('does not treat a registry port as a tag', () => {
      renderList({
        containers: [makeContainer({ id: 'c1', name: 'web', image: 'registry:5000/app' })],
      })

      // The whole reference renders unsplit — ":5000/app" is a registry port,
      // not a tag.
      expect(screen.getAllByText('registry:5000/app').length).toBeGreaterThanOrEqual(2)
    })
  })

  describe('live metrics', () => {
    it('shows CPU and memory for a container with metrics', () => {
      renderList({
        containers: [makeContainer({ id: 'c1', name: 'web' })],
        latestMetrics: { c1: makeMetric({ cpuPercent: 14.06, memUsage: 1.4 * 1024 * 1024 * 1024 }) },
      })

      expect(screen.getAllByText('14.1%').length).toBeGreaterThanOrEqual(1)
      expect(screen.getAllByText('1.40 GB').length).toBeGreaterThanOrEqual(1)
    })

    it('falls back to a name match when the metric id differs', () => {
      renderList({
        containers: [makeContainer({ id: 'c-new', name: 'web' })],
        latestMetrics: { 'c-old': makeMetric({ cpuPercent: 7.5 }) },
        metricNames: [{ containerId: 'c-old', name: 'web', metrics: [] }],
      })

      expect(screen.getAllByText('7.5%').length).toBeGreaterThanOrEqual(1)
    })

    it('shows a dash when no metric has arrived yet', () => {
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web' })] })

      // CPU, Mem and Ports cells all fall back to the dash.
      expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(2)
    })
  })

  describe('row actions', () => {
    it('reports the container NAME for logs and the ID for shell', async () => {
      const user = userEvent.setup()
      const onShowLogs = vi.fn()
      const onOpenShell = vi.fn()
      renderList({
        containers: [makeContainer({ id: 'c1', name: 'web', state: 'running' })],
        onShowLogs,
        onOpenShell,
      })

      await user.click(screen.getAllByRole('button', { name: 'Logs: web' })[0])
      // The log stream filters by container name…
      expect(onShowLogs).toHaveBeenCalledWith('web')

      await user.click(screen.getAllByRole('button', { name: 'Shell: web' })[0])
      // …while the terminal selects by container id.
      expect(onOpenShell).toHaveBeenCalledWith('c1')
    })

    it('disables Shell for a container that is not running', () => {
      renderList({
        containers: [makeContainer({ id: 'c1', name: 'web', state: 'exited' })],
        onOpenShell: vi.fn(),
      })

      for (const btn of screen.getAllByRole('button', { name: 'Shell: web' })) {
        expect(btn).toBeDisabled()
      }
    })

    it('restarts the container through the per-container API', async () => {
      const user = userEvent.setup()
      renderList({ containers: [makeContainer({ id: 'c1', name: 'web' })] })

      await user.click(screen.getAllByRole('button', { name: 'Restart: web' })[0])

      await waitFor(() => expect(restartContainer).toHaveBeenCalledWith('c1'))
    })
  })

  it('renders table headers in desktop view', () => {
    renderList()

    expect(screen.getByText('Service')).toBeInTheDocument()
    expect(screen.getByText('Image')).toBeInTheDocument()
    expect(screen.getByText('State')).toBeInTheDocument()
    expect(screen.getByText('Health')).toBeInTheDocument()
    expect(screen.getByText('CPU')).toBeInTheDocument()
    expect(screen.getByText('Mem')).toBeInTheDocument()
    expect(screen.getByText('Ports')).toBeInTheDocument()
  })
})
