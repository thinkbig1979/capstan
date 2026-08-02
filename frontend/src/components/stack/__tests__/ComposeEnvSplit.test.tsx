import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

// Stub the two heavy editors — their own behaviour is covered by ComposeEditor /
// EnvEditor suites. Here we only assert that the split composes them and wires
// the panel orientation, so we keep stubs lightweight and avoid re-mocking
// codemirror/api/uiStore.
vi.mock('../ComposeEditor', () => ({
  ComposeEditor: ({ stackId }: { stackId: string }) => (
    <div data-testid="compose-editor">compose:{stackId}</div>
  ),
}))
vi.mock('../EnvEditor', () => ({
  EnvEditor: ({ stackId }: { stackId: string }) => (
    <div data-testid="env-editor">env:{stackId}</div>
  ),
}))

import { ComposeEnvSplit } from '../ComposeEnvSplit'

function stubMatchMedia(matches: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      matches,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  )
}

describe('ComposeEnvSplit', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders both editors with the stack id and a draggable handle', async () => {
    stubMatchMedia(true)
    renderWithProviders(<ComposeEnvSplit stackId="my-stack" />)

    // ComposeEditor is now behind a lazy() boundary (bundle-size fix), so its
    // stub resolves asynchronously — the first render pass shows the Suspense
    // fallback instead of the mocked component.
    expect(await screen.findByTestId('compose-editor')).toHaveTextContent('compose:my-stack')
    expect(screen.getByTestId('env-editor')).toHaveTextContent('env:my-stack')
    expect(document.querySelector('[data-slot="resizable-handle"]')).toBeInTheDocument()
  })

  it('lays out horizontally on wide screens', () => {
    stubMatchMedia(true)
    renderWithProviders(<ComposeEnvSplit stackId="s1" />)
    expect(
      document.querySelector('[data-panel-group-direction="horizontal"]'),
    ).toBeInTheDocument()
    // `data-panel-group-direction` is our own wrapper's attribute (it just
    // forwards its own `orientation` prop), so it can't tell a correctly
    // laid-out group from a wrapper that merely claims to be horizontal. The
    // handle's `aria-orientation` comes from the library itself, and is the
    // inverse of the group's real orientation (a horizontal, side-by-side
    // group has a *vertical* divider line) -- so this is the assertion that
    // actually pins the library's layout, not just our wrapper's bookkeeping.
    expect(
      document.querySelector('[data-slot="resizable-handle"][aria-orientation="vertical"]'),
    ).toBeInTheDocument()
  })

  it('stacks vertically on narrow screens', () => {
    stubMatchMedia(false)
    renderWithProviders(<ComposeEnvSplit stackId="s1" />)
    expect(
      document.querySelector('[data-panel-group-direction="vertical"]'),
    ).toBeInTheDocument()
    // See note above: assert the library's own aria-orientation (inverse of
    // the group's orientation) so a wrapper that lies about its own attribute
    // can't fake this test.
    expect(
      document.querySelector('[data-slot="resizable-handle"][aria-orientation="horizontal"]'),
    ).toBeInTheDocument()
  })
})
