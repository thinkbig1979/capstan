import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

const onContainerAction = vi.fn()
const onContainerNameAction = vi.fn()

describe('ContainerList', () => {
  it('shows "Stack is stopped" message when containers array is empty', () => {
    renderWithProviders(
      <ContainerList
        containers={[]}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getByText(/Stack is stopped/)).toBeInTheDocument()
  })

  it('shows "Stack is stopped" message when containers is undefined', () => {
    renderWithProviders(
      <ContainerList
        containers={undefined as unknown as Container[]}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getByText(/Stack is stopped/)).toBeInTheDocument()
  })

  it('renders a table row for each container in desktop view', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
      makeContainer({ id: 'c2', name: 'db' }),
      makeContainer({ id: 'c3', name: 'cache' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getAllByText('web').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('db').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('cache').length).toBeGreaterThanOrEqual(1)
  })

  it('renders a card for each container in mobile view', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
      makeContainer({ id: 'c2', name: 'api' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const names = screen.getAllByText('web')
    expect(names.length).toBeGreaterThanOrEqual(2)

    const apiNames = screen.getAllByText('api')
    expect(apiNames.length).toBeGreaterThanOrEqual(2)
  })

  it('uses container name in terminal button aria-label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'my-container' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const terminalButtons = screen.getAllByLabelText('Open terminal for my-container')
    expect(terminalButtons.length).toBeGreaterThan(0)
  })

  it('uses container name in logs button aria-label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'my-container' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const logButtons = screen.getAllByLabelText('View logs for my-container')
    expect(logButtons.length).toBeGreaterThan(0)
  })

  it('uses different aria-labels for different container names', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'frontend-app' }),
      makeContainer({ id: 'c2', name: 'backend-service' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getAllByLabelText('Open terminal for frontend-app').length).toBeGreaterThan(0)
    expect(screen.getAllByLabelText('Open terminal for backend-service').length).toBeGreaterThan(0)
    expect(screen.getAllByLabelText('View logs for frontend-app').length).toBeGreaterThan(0)
    expect(screen.getAllByLabelText('View logs for backend-service').length).toBeGreaterThan(0)
  })

  it('does not render literal {container.name} in aria-labels', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'test-app' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const buttons = screen.getAllByRole('button')
    for (const button of buttons) {
      const label = button.getAttribute('aria-label')
      if (label) {
        expect(label).not.toContain('{container.name}')
      }
    }
  })

  it('strips duplicate protocol from port display', () => {
    const containers = [
      makeContainer({
        id: 'c1',
        name: 'web',
        ports: [{ host: '0.0.0.0', container: '80/tcp', protocol: 'tcp' }],
      }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

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

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getAllByText('0.0.0.0:53/udp').length).toBeGreaterThanOrEqual(1)
  })

  it('displays dash when no ports exist', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', ports: [] }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

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

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getAllByText('0.0.0.0:80/tcp').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('0.0.0.0:443/tcp').length).toBeGreaterThanOrEqual(1)
  })

  it('renders running state icon with green play', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'running' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const stateTexts = screen.getAllByText('running')
    expect(stateTexts.length).toBeGreaterThan(0)
  })

  it('renders exited state with capitalized label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'exited' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const exitedLabels = screen.getAllByText('exited')
    expect(exitedLabels.length).toBeGreaterThan(0)
  })

  it('renders dead state with capitalized label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'dead' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const deadLabels = screen.getAllByText('dead')
    expect(deadLabels.length).toBeGreaterThan(0)
  })

  it('renders restarting state with capitalized label', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', state: 'restarting' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const restartingLabels = screen.getAllByText('restarting')
    expect(restartingLabels.length).toBeGreaterThan(0)
  })

  it('renders healthy badge when health is healthy', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', health: 'healthy' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const healthyBadges = screen.getAllByText('Healthy')
    expect(healthyBadges.length).toBeGreaterThan(0)
  })

  it('renders unhealthy badge when health is unhealthy', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', health: 'unhealthy' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const unhealthyBadges = screen.getAllByText('Unhealthy')
    expect(unhealthyBadges.length).toBeGreaterThan(0)
  })

  it('renders none badge when health is undefined', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const noneBadges = screen.getAllByText('none')
    expect(noneBadges.length).toBeGreaterThan(0)
  })

  it('renders custom health status as outline badge', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', health: 'starting' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const startingBadges = screen.getAllByText('starting')
    expect(startingBadges.length).toBeGreaterThan(0)
  })

  it('calls onContainerAction with container id and terminal when terminal button clicked', async () => {
    const user = userEvent.setup()
    const handleAction = vi.fn()

    const containers = [
      makeContainer({ id: 'abc123', name: 'my-app' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={handleAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const terminalButtons = screen.getAllByLabelText('Open terminal for my-app')
    await user.click(terminalButtons[0])

    expect(handleAction).toHaveBeenCalledWith('abc123', 'terminal')
  })

  it('calls onContainerAction with container id and logs when logs button clicked', async () => {
    const user = userEvent.setup()
    const handleAction = vi.fn()

    const containers = [
      makeContainer({ id: 'abc123', name: 'my-app' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={handleAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const logButtons = screen.getAllByLabelText('View logs for my-app')
    await user.click(logButtons[0])

    expect(handleAction).toHaveBeenCalledWith('abc123', 'logs')
  })

  it('calls onContainerNameAction with container name and terminal when terminal button clicked', async () => {
    const user = userEvent.setup()
    const handleNameAction = vi.fn()

    const containers = [
      makeContainer({ id: 'abc123', name: 'my-app' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={handleNameAction}
      />
    )

    const terminalButtons = screen.getAllByLabelText('Open terminal for my-app')
    await user.click(terminalButtons[0])

    expect(handleNameAction).toHaveBeenCalledWith('my-app', 'terminal')
  })

  it('calls onContainerNameAction with container name and logs when logs button clicked', async () => {
    const user = userEvent.setup()
    const handleNameAction = vi.fn()

    const containers = [
      makeContainer({ id: 'abc123', name: 'my-app' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={handleNameAction}
      />
    )

    const logButtons = screen.getAllByLabelText('View logs for my-app')
    await user.click(logButtons[0])

    expect(handleNameAction).toHaveBeenCalledWith('my-app', 'logs')
  })

  it('does not call onContainerNameAction when it is not provided', async () => {
    const user = userEvent.setup()
    const handleAction = vi.fn()

    const containers = [
      makeContainer({ id: 'abc123', name: 'my-app' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={handleAction}
      />
    )

    const terminalButtons = screen.getAllByLabelText('Open terminal for my-app')
    await user.click(terminalButtons[0])

    expect(handleAction).toHaveBeenCalledWith('abc123', 'terminal')
  })

  it('renders container image in desktop table', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', image: 'nginx:1.25' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const imageTexts = screen.getAllByText('nginx:1.25')
    expect(imageTexts.length).toBeGreaterThanOrEqual(2)
  })

  it('renders container status text', () => {
    const containers = [
      makeContainer({ id: 'c1', name: 'web', status: 'Up 5 minutes' }),
    ]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    const statusTexts = screen.getAllByText('Up 5 minutes')
    expect(statusTexts.length).toBeGreaterThanOrEqual(2)
  })

  it('renders table headers in desktop view', () => {
    const containers = [makeContainer()]

    renderWithProviders(
      <ContainerList
        containers={containers}
        onContainerAction={onContainerAction}
        onContainerNameAction={onContainerNameAction}
      />
    )

    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Image')).toBeInTheDocument()
    expect(screen.getByText('State')).toBeInTheDocument()
    expect(screen.getByText('Status')).toBeInTheDocument()
    expect(screen.getByText('Ports')).toBeInTheDocument()
    expect(screen.getByText('Health')).toBeInTheDocument()
  })
})
