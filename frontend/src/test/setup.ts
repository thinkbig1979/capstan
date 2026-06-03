import '@testing-library/jest-dom'

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
