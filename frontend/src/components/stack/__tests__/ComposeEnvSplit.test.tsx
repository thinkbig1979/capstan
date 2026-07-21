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
  })

  it('stacks vertically on narrow screens', () => {
    stubMatchMedia(false)
    renderWithProviders(<ComposeEnvSplit stackId="s1" />)
    expect(
      document.querySelector('[data-panel-group-direction="vertical"]'),
    ).toBeInTheDocument()
  })
})
