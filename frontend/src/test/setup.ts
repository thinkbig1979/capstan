import '@testing-library/jest-dom'
import { afterEach, vi } from 'vitest'

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
// on the jsdom container (focus-scope/dist/index.mjs:94-103). RTL's afterEach
// cleanup unmounts every rendered tree, which ARMS that timer for any test that
// left a Dialog/Popover/DropdownMenu mounted; if the file ends and vitest tears
// the jsdom realm down before the timer fires, `CustomEvent` is Node's native
// class again and jsdom rejects the dispatch as an Uncaught Exception ("parameter
// 1 is not of type 'Event'"): every test green, `Errors 1 error`, exit 1.
// Draining one real macrotask here lets that timer fire while the realm is
// still installed. This hook is registered before RTL's, and vitest runs
// afterEach in reverse registration order (sequence.hooks defaults to
// "stack"), so it runs AFTER cleanup has armed the timer. Under fake timers
// there is nothing to drain: a pending fake timer is discarded, never fired,
// when the clock is uninstalled or the file ends.
afterEach(async () => {
  if (vi.isFakeTimers()) return
  await new Promise<void>((resolve) => setTimeout(resolve, 0))
})
