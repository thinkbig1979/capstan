import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AutoUpdateToggle } from '../AutoUpdateToggle'
import { toGlobalAutoUpdateState } from '../auto-update-state'

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
        globalState="enabled"
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
        globalState="enabled"
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
        globalState="enabled"
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
        globalState="enabled"
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
        globalState="enabled"
      />
    )
    expect(screen.getByText('2f')).toBeInTheDocument()
  })

  /**
   * agent-os-bueb. `globalDisabled` was a boolean, so a policies query that
   * FAILED and a master switch the operator deliberately turned off rendered
   * the same locked tooltip — one of which sent the operator to Settings to
   * change a setting that was never the cause.
   *
   * All three arms assert the aria-label, not the tooltip copy: Radix renders
   * TooltipContent in a portal that stays out of the DOM until the trigger is
   * hovered or focused, so a queryByText over the copy is null either way.
   */
  it.each([
    ['disabled', 'global auto-update is off'],
    ['loading', 'checking global auto-update'],
    ['unavailable', 'auto-update policy state unavailable'],
  ] as const)('locks and explains %s', (globalState, reason) => {
    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={false}
        consecutiveFailures={0}
        globalState={globalState}
      />
    )
    const switchEl = screen.getByRole('switch')
    expect(switchEl).toBeDisabled()
    expect(switchEl).toHaveAttribute(
      'aria-label',
      `Auto-update container abc123 (locked, ${reason})`,
    )
  })

  it('only the deliberately-off state blames the global switch', () => {
    const { unmount } = render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={false}
        consecutiveFailures={0}
        globalState="unavailable"
      />
    )
    expect(screen.getByRole('switch').getAttribute('aria-label')).not.toContain(
      'global auto-update is off',
    )
    unmount()

    render(
      <AutoUpdateToggle
        targetType="container"
        targetId="abc123"
        enabled={true}
        paused={false}
        consecutiveFailures={0}
        globalState="disabled"
      />
    )
    expect(screen.getByRole('switch').getAttribute('aria-label')).toContain(
      'global auto-update is off',
    )
  })
})

describe('toGlobalAutoUpdateState', () => {
  it('reports a failed query as unavailable, never as disabled', () => {
    expect(toGlobalAutoUpdateState({ isPending: false, isError: true })).toBe('unavailable')
  })

  it('reports an in-flight query as loading', () => {
    expect(toGlobalAutoUpdateState({ isPending: true, isError: false })).toBe('loading')
  })

  it('reports a settled query with no data as loading, not disabled', () => {
    expect(toGlobalAutoUpdateState({ isPending: false, isError: false })).toBe('loading')
  })

  it('reports a resolved globalEnabled false as disabled', () => {
    expect(
      toGlobalAutoUpdateState({ data: { globalEnabled: false }, isPending: false, isError: false }),
    ).toBe('disabled')
  })

  it('reports a resolved globalEnabled true as enabled', () => {
    expect(
      toGlobalAutoUpdateState({ data: { globalEnabled: true }, isPending: false, isError: false }),
    ).toBe('enabled')
  })
})
