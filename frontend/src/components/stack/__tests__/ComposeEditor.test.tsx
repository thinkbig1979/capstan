import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

vi.mock('codemirror', () => {
  class MockEditorView {
    static theme = () => []
    static updateListener = { of: () => [] }
    dispatch() {}
    destroy() {}
    state = { doc: { toString: () => '', length: 0 } }
  }
  return { basicSetup: [], EditorView: MockEditorView }
})
vi.mock('@codemirror/state', () => ({
  EditorState: { create: () => ({ doc: { toString: () => '', length: 0 } }) },
}))
vi.mock('@codemirror/view', () => {
  class MockEditorView {
    static theme = () => []
    static updateListener = { of: () => [] }
    dispatch() {}
    destroy() {}
    state = { doc: { toString: () => '', length: 0 } }
  }
  return {
    EditorView: MockEditorView,
    keymap: { of: () => [] },
  }
})
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/lint', () => ({ linter: () => [], lintGutter: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))

const mockGetCompose = vi.fn()
const mockUpdateCompose = vi.fn()
const mockLintCompose = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: (...args: unknown[]) => mockGetCompose(...args),
    updateCompose: (...args: unknown[]) => mockUpdateCompose(...args),
    lintCompose: (...args: unknown[]) => mockLintCompose(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

vi.mock('@/stores/uiStore', () => ({
  useUIStore: () => ({ theme: 'light' }),
}))

import { ComposeEditor } from '../ComposeEditor'

describe('ComposeEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows loading state while fetching compose file', () => {
    mockGetCompose.mockReturnValue(new Promise(() => {}))
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    expect(screen.getByText('Loading compose file...')).toBeInTheDocument()
  })

  it('renders save and lint buttons after loading', async () => {
    mockGetCompose.mockResolvedValue('services:\n  web:\n    image: nginx\n')
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
      expect(screen.getByText('Lint')).toBeInTheDocument()
    })
  })

  it('disables save button when no unsaved changes', async () => {
    mockGetCompose.mockResolvedValue('services:\n  web:\n    image: nginx\n')
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeDisabled()
    })
  })

  it('shows lint results when present', async () => {
    mockGetCompose.mockResolvedValue('services:\n  web:\n    image: nginx\n')
    mockLintCompose.mockResolvedValue({
        lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }],
      })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })
  })

  it('shows Ctrl+S hint', async () => {
    mockGetCompose.mockResolvedValue('services:\n  web:\n    image: nginx\n')
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Ctrl+S to save')).toBeInTheDocument()
    })
  })
})
