import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AutoUpdateToggle } from '../AutoUpdateToggle'

const mockMutate = vi.fn()
vi.mock('@/hooks/useResources', () => ({
  useToggleAutoUpdate: () => ({
    mutate: mockMutate,
    isPending: false,
  }),
}))

describe('AutoUpdateToggle', () => {
  it('renders switch in checked state when enabled and not paused', () => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={false}
        consecutiveFailures={0}
      />
    )
    const switchEl = screen.getByRole('switch')
    expect(switchEl).toBeChecked()
  })

  it('renders switch in unchecked state when disabled', () => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={false}
        paused={false}
        consecutiveFailures={0}
      />
    )
    const switchEl = screen.getByRole('switch')
    expect(switchEl).not.toBeChecked()
  })

  it('calls mutation on toggle', () => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={false}
        paused={false}
        consecutiveFailures={0}
      />
    )
    fireEvent.click(screen.getByRole('switch'))
    expect(mockMutate).toHaveBeenCalledWith(
      { targetType: 'container', targetId: 'abc123', enabled: true },
      expect.objectContaining({ onError: expect.any(Function) })
    )
  })

  it('shows paused indicator when paused', () => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={true}
        consecutiveFailures={0}
      />
    )
    expect(screen.getByRole('switch')).not.toBeChecked()
  })

  it('shows failure count when has failures but not paused', () => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={false}
        consecutiveFailures={2}
      />
    )
    expect(screen.getByText('2f')).toBeInTheDocument()
  })

  it('shows locked state when globalDisabled', () => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={false}
        consecutiveFailures={0}
        globalDisabled={true}
      />
    )
    const switchEl = screen.getByRole('switch')
    expect(switchEl).toBeDisabled()
  })
})
