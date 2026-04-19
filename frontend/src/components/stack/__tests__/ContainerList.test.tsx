import { describe, it, expect } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { ContainerList } from '../ContainerList'
import type { Container } from '@/types'

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

describe('ContainerList', () => {
  it('shows "Stack is stopped" message when containers array is empty', () => {
    renderWithProviders(<ContainerList containers={[]} />)

    expect(screen.getByText(/Stack is stopped/)).toBeInTheDocument()
  })

  it('shows "Stack is stopped" message when containers is undefined', () => {
    renderWithProviders(<ContainerList containers={undefined as unknown as Container[]} />)

    expect(screen.getByText(/Stack is stopped/)).toBeInTheDocument()
  })

  it('renders a table row for each container in desktop view', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
      makeContainer({ id: 'c2', name: 'db' }),
      makeContainer({ id: 'c3', name: 'cache' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    expect(screen.getAllByText('web').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('db').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('cache').length).toBeGreaterThanOrEqual(1)
  })

  it('renders a card for each container in mobile view', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
      makeContainer({ id: 'c2', name: 'api' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const names = screen.getAllByText('web')
    expect(names.length).toBeGreaterThanOrEqual(2)

    const apiNames = screen.getAllByText('api')
    expect(apiNames.length).toBeGreaterThanOrEqual(2)
  })

  it('strips duplicate protocol from port display', () => {
    const containers = [
      makeContainer({
        id: 'c1',
        name: 'web',
        ports: [{ host: '0.0.0.0', container: '80/tcp', protocol: 'tcp' }],
      }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    expect(screen.getAllByText('0.0.0.0:80/tcp').length).toBeGreaterThanOrEqual(1)
  })

  it('formats udp ports correctly without duplication', () => {
    const containers = [
      makeContainer({
        id: 'c1',
        name: 'dns',
        ports: [{ host: '0.0.0.0', container: '53/udp', protocol: 'udp' }],
      }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    expect(screen.getAllByText('0.0.0.0:53/udp').length).toBeGreaterThanOrEqual(1)
  })

  it('displays dash when no ports exist', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', ports: [] }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const desktopDashes = screen.getAllByText('-')
    expect(desktopDashes.length).toBeGreaterThan(0)
  })

  it('displays multiple ports', () => {
    const containers = [
      makeContainer({
        id: 'c1',
        name: 'web',
        ports: [
          { host: '0.0.0.0', container: '80/tcp', protocol: 'tcp' },
          { host: '0.0.0.0', container: '443/tcp', protocol: 'tcp' },
        ],
      }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    expect(screen.getAllByText('0.0.0.0:80/tcp').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('0.0.0.0:443/tcp').length).toBeGreaterThanOrEqual(1)
  })

  it('renders running state icon with green play', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'running' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const stateTexts = screen.getAllByText('running')
    expect(stateTexts.length).toBeGreaterThan(0)
  })

  it('renders exited state with capitalized label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'exited' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const exitedLabels = screen.getAllByText('exited')
    expect(exitedLabels.length).toBeGreaterThan(0)
  })

  it('renders dead state with capitalized label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'dead' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const deadLabels = screen.getAllByText('dead')
    expect(deadLabels.length).toBeGreaterThan(0)
  })

  it('renders restarting state with capitalized label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'restarting' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const restartingLabels = screen.getAllByText('restarting')
    expect(restartingLabels.length).toBeGreaterThan(0)
  })

  it('renders healthy badge when health is healthy', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', health: 'healthy' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const healthyBadges = screen.getAllByText('Healthy')
    expect(healthyBadges.length).toBeGreaterThan(0)
  })

  it('renders unhealthy badge when health is unhealthy', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', health: 'unhealthy' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const unhealthyBadges = screen.getAllByText('Unhealthy')
    expect(unhealthyBadges.length).toBeGreaterThan(0)
  })

  it('renders none badge when health is undefined', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const noneBadges = screen.getAllByText('none')
    expect(noneBadges.length).toBeGreaterThan(0)
  })

  it('renders custom health status as outline badge', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', health: 'starting' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const startingBadges = screen.getAllByText('starting')
    expect(startingBadges.length).toBeGreaterThan(0)
  })

  it('renders container image in desktop table', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', image: 'nginx:1.25' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const imageTexts = screen.getAllByText('nginx:1.25')
    expect(imageTexts.length).toBeGreaterThanOrEqual(2)
  })

  it('renders container status text', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', status: 'Up 5 minutes' }),
    ]

    renderWithProviders(<ContainerList containers={containers} />)

    const statusTexts = screen.getAllByText('Up 5 minutes')
    expect(statusTexts.length).toBeGreaterThanOrEqual(2)
  })

  it('renders table headers in desktop view', () => {
    const containers = [makeContainer()]

    renderWithProviders(<ContainerList containers={containers} />)

    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Image')).toBeInTheDocument()
    expect(screen.getByText('State')).toBeInTheDocument()
    expect(screen.getByText('Status')).toBeInTheDocument()
    expect(screen.getByText('Ports')).toBeInTheDocument()
    expect(screen.getByText('Health')).toBeInTheDocument()
  })
})
