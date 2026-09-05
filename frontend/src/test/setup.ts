import '@testing-library/jest-dom'
import { afterEach, vi } from 'vitest'
import { act, cleanup } from '@testing-library/react'

// cmdk uses ResizeObserver internally; jsdom doesn't implement it.
if (typeof ResizeObserver === 'undefined') {
  ;(globalThis as typeof globalThis & { ResizeObserver: unknown }).ResizeObserver =
    class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
}

// cmdk calls scrollIntoView on list items; jsdom doesn't implement it.
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}

// agent-os-thvu: @radix-ui/react-focus-scope's unmount cleanup arms a 0 ms
// setTimeout that builds `new CustomEvent(...)` at fire time and dispatches it
// on the jsdom container (focus-scope/dist/index.mjs:94-103). RTL's cleanup
// unmounts every rendered tree, which ARMS that timer for any test that left a
// Dialog/Popover/DropdownMenu mounted; if the file ends and vitest tears the
// jsdom realm down before the timer fires, `CustomEvent` is Node's native
// class again and jsdom rejects the dispatch as an Uncaught Exception ("parameter
// 1 is not of type 'Event'"): every test green, `Errors 1 error`, exit 1.
// So: run cleanup() here ourselves (idempotent, and RTL's own afterEach may
// run before or after this one; sequence.hooks is a config default, not a
// contract), then drain one real macrotask inside act() so unmount effects
// flush and the timer fires while the realm is still installed. Under fake
// timers there is nothing to drain: a pending fake timer is discarded, never
// fired, when the clock is uninstalled or the file ends. vi.runOnlyPendingTimers()
// would fire it too, but also any timer a test deliberately left armed.
afterEach(async () => {
  cleanup()
  if (vi.isFakeTimers()) return
  await act(async () => {
    await new Promise<void>((resolve) => setTimeout(resolve, 0))
  })
})
