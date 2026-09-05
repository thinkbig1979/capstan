/**
 * agent-os-thvu: @radix-ui/react-focus-scope arms a 0 ms setTimeout in its
 * unmount cleanup that builds `new CustomEvent(...)` AT FIRE TIME and
 * dispatches it on the (jsdom) container. If the test environment has been
 * torn down in between, `CustomEvent` is Node's native class again and jsdom
 * rejects the dispatch: "parameter 1 is not of type 'Event'".
 *
 * Reproduction shape: test A mounts an open Dialog and ends without
 * unmounting (RTL's afterEach cleanup unmounts it, which ARMS the timer).
 * Test B then does what vitest's jsdom teardown does to the realm, restore a
 * non-jsdom Event class on `globalThis.CustomEvent`, and advances real time
 * one tick. The realm swap MODELS the environment teardown (the real one is
 * a race against the pool's "stop" round trip and could not be forced from a
 * test file, 10/10 clean runs across forks and threads); the timer, the
 * Dialog, the unmount path and the jsdom rejection are all the real thing.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Dialog, DialogContent, DialogTitle, DialogTrigger, DialogClose } from '@/components/ui/dialog'

// Node's own Event class, reached through AbortSignal (vitest leaves
// AbortController as Node's; only jsdom's living keys are swapped in).
function nodeRealmEvent(): typeof Event {
  let ctor: typeof Event | undefined
  const ac = new AbortController()
  ac.signal.addEventListener('abort', (e) => {
    ctor = e.constructor as typeof Event
  })
  ac.abort()
  if (!ctor) throw new Error('AbortSignal did not dispatch')
  return ctor
}

describe('radix focus-scope unmount timer vs environment teardown', () => {
  it('focus-restore control: closing the dialog returns focus to the trigger', async () => {
    render(
      <Dialog>
        <DialogTrigger>open me</DialogTrigger>
        <DialogContent>
          <DialogTitle>probe</DialogTitle>
          <DialogClose>close me</DialogClose>
        </DialogContent>
      </Dialog>,
    )
    const trigger = screen.getByText('open me')
    trigger.focus()
    fireEvent.click(trigger)
    expect(await screen.findByText('probe')).toBeInTheDocument()
    expect(trigger).not.toHaveFocus()
    fireEvent.click(screen.getByText('close me'))
    await waitFor(() => expect(screen.queryByText('probe')).not.toBeInTheDocument())
    // The restore runs in FocusScope's 0 ms timer, so it needs a real tick.
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('A: mounts an open Dialog and ends WITHOUT unmounting', () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>probe</DialogTitle>
        </DialogContent>
      </Dialog>,
    )
    expect(screen.getByText('probe')).toBeInTheDocument()
    // RTL's afterEach cleanup unmounts this and arms the focus-scope timer.
  })

  it('B: the realm is gone and one real tick passes', async () => {
    const NodeEvent = nodeRealmEvent()
    expect(NodeEvent).not.toBe(globalThis.Event)
    const jsdomCustomEvent = globalThis.CustomEvent
    // What vitest's jsdom env teardown does: `originals.forEach((v, k) => global[k] = v)`.
    globalThis.CustomEvent = NodeEvent as unknown as typeof CustomEvent
    try {
      await new Promise<void>((resolve) => setTimeout(resolve, 5))
    } finally {
      globalThis.CustomEvent = jsdomCustomEvent
    }
    expect(vi.isFakeTimers()).toBe(false)
  })
})
