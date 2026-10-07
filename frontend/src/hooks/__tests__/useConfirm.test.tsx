/**
 * agent-os-z91e.11: a second confirm() while a dialog is pending used to
 * overwrite the stored resolver, so the FIRST caller's promise could never
 * settle and its code after the await never ran. It must settle as declined.
 *
 * "Never settles" is asserted through a short explicit bound (Promise.race
 * against a sentinel) so the failing run is an assertion, not a test timeout.
 */
import { describe, it, expect } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useConfirm } from '../useConfirm'

const PENDING = 'pending' as const
const BOUND_MS = 300

function settled<T>(p: Promise<T>): Promise<T | typeof PENDING> {
  return Promise.race([
    p,
    new Promise<typeof PENDING>((r) => setTimeout(() => r(PENDING), BOUND_MS)),
  ])
}

// The hook's forceUpdate re-renders the component that CALLS useConfirm, so the
// dialog must be rendered by that same component, as every real caller does.
function setup() {
  const ref: { current: ReturnType<typeof useConfirm>['confirm'] | null } = { current: null }
  function Host() {
    const { confirm, ConfirmComponent } = useConfirm()
    ref.current = confirm
    return <ConfirmComponent />
  }
  render(<Host />)
  return (title: string, description: string) => ref.current!(title, description)
}

describe('useConfirm', () => {
  it('a single confirm resolves true on Confirm and false on Cancel (controls)', async () => {
    const user = userEvent.setup()
    const confirm = setup()

    let p!: Promise<boolean>
    act(() => { p = confirm('Title', 'Body') })
    await user.click(await screen.findByRole('button', { name: 'Confirm' }))
    expect(await settled(p)).toBe(true)

    act(() => { p = confirm('Title', 'Body') })
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(await settled(p)).toBe(false)
  })

  it('a second confirm settles the first as declined and shows only the second dialog', async () => {
    const user = userEvent.setup()
    const confirm = setup()

    let p1!: Promise<boolean>
    let p2!: Promise<boolean>
    act(() => { p1 = confirm('First', 'first body') })
    act(() => { p2 = confirm('Second', 'second body') })

    // The first caller is released with false before any user action.
    expect(await settled(p1)).toBe(false)

    // One dialog, and it is the second call's.
    expect(screen.queryByText('First')).toBeNull()
    expect(await screen.findByText('Second')).toBeTruthy()

    // The second call still resolves from the user's answer.
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    expect(await settled(p2)).toBe(true)
  })

  it('declining the second dialog resolves the second as false and leaves the first at false', async () => {
    const user = userEvent.setup()
    const confirm = setup()

    let p1!: Promise<boolean>
    let p2!: Promise<boolean>
    act(() => { p1 = confirm('First', 'a') })
    act(() => { p2 = confirm('Second', 'b') })

    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(await settled(p2)).toBe(false)
    expect(await settled(p1)).toBe(false)
  })
})
