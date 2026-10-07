/**
 * agent-os-z91e.41: a theme change must reconfigure the live editor, not
 * rebuild it. useCodeMirrorEditor used to list `isDark` in the deps of the
 * effect that creates the EditorView, so the in-app theme toggle destroyed the
 * view and built a new one from `options.doc`. ComposeEditor passes the SAVED
 * file there (`data || content`), so unsaved edits, the cursor and the undo
 * history were silently replaced by the saved text.
 *
 * Every other test of these editors mocks CodeMirror out, so none of them can
 * see a rebuild. This file mocks NO CodeMirror module: the assertions read the
 * real EditorView's document, identity and dark-theme facet. Only the API
 * layer is faked.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, screen, waitFor, render } from '@testing-library/react'
import { useState } from 'react'
import { EditorView } from 'codemirror'
import { renderWithProviders } from '../../test/utils'
import { useUIStore } from '@/stores/uiStore'

const SAVED = 'services:\n  web:\n    image: nginx\n'
const EDITED = 'services:\n  web:\n    image: nginx:edited\n'

const mockGetCompose = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: (...args: unknown[]) => mockGetCompose(...args),
    updateCompose: vi.fn(),
    lintCompose: vi.fn(),
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ComposeEditor } from '@/components/stack/ComposeEditor'
import { useComposeEditorMount } from '@/components/stack/create-stack/useComposeEditorMount'

function liveView(container: ParentNode): EditorView {
  const dom = container.querySelector('.cm-editor')
  if (!dom) throw new Error('no .cm-editor in the document: the editor was not created')
  const view = EditorView.findFromDOM(dom as HTMLElement)
  if (!view) throw new Error('.cm-editor has no EditorView behind it')
  return view
}

function replaceDoc(view: EditorView, text: string) {
  act(() => {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } })
  })
}

describe('theme change keeps the live editor (agent-os-z91e.41)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useUIStore.getState().setTheme('light'))
  })

  afterEach(() => {
    act(() => useUIStore.getState().setTheme('light'))
  })

  it('ComposeEditor keeps unsaved text and the same view when the theme flips', async () => {
    mockGetCompose.mockResolvedValue({ content: SAVED })
    const { container } = renderWithProviders(<ComposeEditor stackId="s1" />)

    await waitFor(() => expect(liveView(container).state.doc.toString()).toBe(SAVED))
    const before = liveView(container)
    replaceDoc(before, EDITED)
    expect(before.state.doc.toString()).toBe(EDITED)
    expect(before.state.facet(EditorView.darkTheme)).toBe(false)

    act(() => useUIStore.getState().setTheme('dark'))

    const after = liveView(container)
    // The user-visible loss: the edit reverted to the saved file.
    expect(after.state.doc.toString()).toBe(EDITED)
    expect(after).toBe(before)
    // And the theme did change, so the test cannot pass by ignoring it.
    expect(after.state.facet(EditorView.darkTheme)).toBe(true)

    act(() => useUIStore.getState().setTheme('light'))
    expect(liveView(container)).toBe(before)
    expect(before.state.doc.toString()).toBe(EDITED)
    expect(before.state.facet(EditorView.darkTheme)).toBe(false)
    expect(screen.queryByText(/Loading compose file/)).toBeNull()
  })

  // The create-stack editor never lost text: its `doc` is the edited
  // composeContent, so a rebuild restored it. It still rebuilt the view (and
  // with it the cursor and undo history), so identity is the assertion that
  // discriminates here.
  it('create-stack editor keeps the same view when the theme flips', () => {
    function Harness() {
      const [composeContent, setComposeContent] = useState(SAVED)
      const [pendingCompose, setPendingCompose] = useState<string | null>(null)
      const [editorEpoch, setEditorEpoch] = useState(0)
      const { editorRef } = useComposeEditorMount({
        open: true,
        composeTab: 'editor',
        editorEpoch,
        setEditorEpoch,
        composeContent,
        setComposeContent,
        pendingCompose,
        setPendingCompose,
      })
      return <div ref={editorRef} />
    }

    const { container } = render(<Harness />)
    // The mount hook bumps editorEpoch once on a frame after `open`; let it
    // settle so the view under test is the one that stays.
    return waitFor(() => {
      expect(liveView(container).state.doc.toString()).toBe(SAVED)
    }).then(async () => {
      await act(async () => {
        await new Promise((r) => requestAnimationFrame(() => r(null)))
      })
      const before = liveView(container)
      replaceDoc(before, EDITED)
      await act(async () => {
        await new Promise((r) => requestAnimationFrame(() => r(null)))
      })

      act(() => useUIStore.getState().setTheme('dark'))

      const after = liveView(container)
      expect(after.state.doc.toString()).toBe(EDITED)
      expect(after).toBe(before)
      expect(after.state.facet(EditorView.darkTheme)).toBe(true)
    })
  })
})
