import * as jestDomMatchers from '@testing-library/jest-dom/matchers'
import type { TestingLibraryMatchers } from '@testing-library/jest-dom/matchers'
import { afterEach, expect, vi } from 'vitest'
import { act, cleanup } from '@testing-library/react'

// Vitest 5 no longer reads matcher types from the global `jest.Matchers`
// interface that '@testing-library/jest-dom' augments, and jest-dom 7.0.1's own
// '/vitest' entry still declares the one-parameter `Assertion<T>` that vitest 5
// replaced with `Assertion<R, T>`. So register the matchers on vitest's expect
// and augment `Matchers<R, T>`, the documented vitest 5 extension point
// (https://vitest.dev/guide/migration). The type parameters must match
// vitest's declaration exactly for the interfaces to merge.
expect.extend(jestDomMatchers)

declare module 'vitest' {
  // Merging needs vitest's exact parameter list, unused `T` included, and adds
  // no members of its own: both lint rules are inherent to the augmentation.
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type, @typescript-eslint/no-unused-vars
  interface Matchers<R extends void | Promise<void> = void | Promise<void>, T = unknown>
    extends TestingLibraryMatchers<unknown, R> {}
}

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
