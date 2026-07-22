/**
 * Reproduces the dead Save button bug: hasUnsavedChanges compares `content`
 * against `lastSaved`, but typing in codemirror never updated React `content`
 * state (the useCodeMirrorEditor call in ComposeEditor.tsx did not pass
 * `onChange`), so the toolbar Save button could never enable from typing
 * alone — see ComposeEditor.tsx and ComposeEditor.save-lint.test.tsx's file
 * header, which documented this as a known quirk.
 *
 * Mocks useCodeMirrorEditor the same way ComposeEditor.save-lint.test.tsx
 * does, capturing the options passed by ComposeEditor so the test can invoke
 * onChange directly to simulate a codemirror doc change.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useRef } from 'react'

const mockViewState = {
  doc: {
    toString: () => 'services:\n  web:\n    image: nginx\n',
    length: 40,
  },
  selection: {
    main: { from: 0, to: 0 },
  },
}
const mockDispatch = vi.fn()

let capturedOnChange: ((content: string) => void) | undefined

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn((_ref: unknown, opts: { onChange?: (content: string) => void }) => {
    const viewRef = useRef<{
      state: typeof mockViewState
      dispatch: typeof mockDispatch
      destroy: () => void
    } | null>(null)
    viewRef.current = { state: mockViewState, dispatch: mockDispatch, destroy: vi.fn() }
    capturedOnChange = opts.onChange
    return { viewRef, isDark: false }
  }),
}))

vi.mock('codemirror', () => ({ basicSetup: [], EditorView: class { destroy() {} } }))
vi.mock('@codemirror/state', () => ({ EditorState: { create: () => ({}) } }))
vi.mock('@codemirror/view', () => ({
  EditorView: class { destroy() {} },
  keymap: { of: () => [] },
}))
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/lint', () => ({ linter: () => [], lintGutter: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))
vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

const mockApiGet = vi.fn()
const mockApiPut = vi.fn()
const mockApiPost = vi.fn()

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: (...args: unknown[]) => mockApiGet(...args),
    put: (...args: unknown[]) => mockApiPut(...args),
    post: (...args: unknown[]) => mockApiPost(...args),
  },
  stacksApi: {
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ComposeEditor } from '../ComposeEditor'

describe('ComposeEditor — Save button tracks editor dirtiness', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockDispatch.mockReset()
    capturedOnChange = undefined
    mockApiGet.mockResolvedValue({ data: 'services:\n  web:\n    image: nginx\n' })
  })

  it('enables the Save button once the editor content diverges from last saved (typing)', async () => {
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() => expect(screen.getByText('Save')).toBeDisabled())
    await waitFor(() => expect(capturedOnChange).toBeDefined())

    act(() => {
      capturedOnChange?.('services:\n  web:\n    image: nginx:1.25\n')
    })

    await waitFor(() => expect(screen.getByText('Save')).not.toBeDisabled())
  })

  it('does not re-enable the Save button immediately after a successful save', async () => {
    mockApiPost.mockResolvedValue({ data: { lintResults: [] } })
    mockApiPut.mockResolvedValue({ data: { lintResults: [] } })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnChange).toBeDefined())

    const edited = 'services:\n  web:\n    image: nginx:1.25\n'
    act(() => {
      capturedOnChange?.(edited)
    })
    await waitFor(() => expect(screen.getByText('Save')).not.toBeDisabled())

    mockViewState.doc.toString = () => edited

    await act(async () => {
      screen.getByText('Save').click()
    })

    await waitFor(() => expect(mockApiPut).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByText('Save')).toBeDisabled())
  })
})
