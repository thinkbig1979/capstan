import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render } from '@testing-library/react'
import { Sparkline } from '../Sparkline'

// recharts ResponsiveContainer needs a DOM size to render — provide minimal stubs.
beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: () => ({ width: 100, height: 32, top: 0, left: 0, bottom: 32, right: 100, x: 0, y: 0, toJSON: () => {} }),
  })
  // ResizeObserver is not in jsdom
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
})

// recharts renders SVG — mock getComputedStyle so CSS var resolution doesn't crash.
vi.stubGlobal('getComputedStyle', () => ({
  getPropertyValue: () => '#22c55e',
}))

describe('Sparkline', () => {
  it('renders an svg element for a normal series', () => {
    const { container } = render(
      <Sparkline series={[10, 20, 30, 40, 50]} width={80} height={32} />,
    )
    const svg = container.querySelector('svg')
    expect(svg).not.toBeNull()
  })

  it('renders a path element for a series with data', () => {
    const { container } = render(
      <Sparkline series={[5, 15, 25]} width={80} height={32} />,
    )
    // recharts renders <path> elements for the area and stroke
    const paths = container.querySelectorAll('path')
    expect(paths.length).toBeGreaterThan(0)
  })

  it('handles an empty series without crashing', () => {
    expect(() =>
      render(<Sparkline series={[]} width={80} height={32} />),
    ).not.toThrow()
  })

  it('handles a single-point series without crashing', () => {
    expect(() =>
      render(<Sparkline series={[42]} width={80} height={32} />),
    ).not.toThrow()
  })

  it('renders a wrapping div with specified dimensions', () => {
    const { container } = render(
      <Sparkline series={[1, 2, 3]} width={60} height={24} />,
    )
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.style.width).toBe('60px')
    expect(wrapper.style.height).toBe('24px')
  })

  it('accepts a string width (e.g. 100%)', () => {
    const { container } = render(
      <Sparkline series={[1, 2, 3]} width="100%" height={32} />,
    )
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.style.width).toBe('100%')
  })

  it('applies className to the wrapper div', () => {
    const { container } = render(
      <Sparkline series={[1, 2, 3]} className="my-custom-class" />,
    )
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.className).toContain('my-custom-class')
  })
})
