import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

vi.mock('codemirror', () => ({
  basicSetup: [],
  EditorView: class {
    static theme = () => []
    dispatch() {}
    destroy() {}
    state = { doc: { toString: () => '', length: 0 } }
  },
}))
vi.mock('@codemirror/state', () => ({
  EditorState: { create: () => ({ doc: { toString: () => '', length: 0 } }) },
}))
vi.mock('@codemirror/view', () => ({
  EditorView: class {
    static theme = () => []
    dispatch() {}
    destroy() {}
    state = { doc: { toString: () => '', length: 0 } }
  },
  keymap: { of: () => [] },
}))
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/lint', () => ({ linter: () => [], lintGutter: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))

const mockGet = vi.fn()
const mockPut = vi.fn()
const mockPost = vi.fn()

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: (...args: unknown[]) => mockGet(...args),
    put: (...args: unknown[]) => mockPut(...args),
    post: (...args: unknown[]) => mockPost(...args),
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
    mockGet.mockReturnValue(new Promise(() => {}))
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    expect(screen.getByText('Loading compose file...')).toBeInTheDocument()
  })

  it('renders save and lint buttons after loading', async () => {
    mockGet.mockResolvedValue({
      data: 'services:\n  web:\n    image: nginx\n',
    })
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
      expect(screen.getByText('Lint')).toBeInTheDocument()
    })
  })

  it('disables save button when no unsaved changes', async () => {
    mockGet.mockResolvedValue({
      data: 'services:\n  web:\n    image: nginx\n',
    })
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeDisabled()
    })
  })

  it('shows lint results when present', async () => {
    mockGet.mockResolvedValue({ data: 'services:\n  web:\n    image: nginx\n' })
    mockPost.mockResolvedValue({
      data: {
        lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }],
      },
    })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })
  })

  it('shows Ctrl+S hint', async () => {
    mockGet.mockResolvedValue({ data: 'services:\n  web:\n    image: nginx\n' })
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => {
      expect(screen.getByText('Ctrl+S to save')).toBeInTheDocument()
    })
  })
})
