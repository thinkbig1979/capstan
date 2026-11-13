/**
 * agent-os-w6wx review (cor D2): a server-driven replacement of the editor's
 * text was an ordinary, undoable edit. After an unlock replaced the masked
 * text with the real file, Ctrl+Z brought "***" back into an EDITABLE
 * editor, and a save then wrote it over the secret. Every replacement now
 * starts a fresh undo history.
 *
 * The real useCodeMirrorEditor and CodeMirror run here (no hook mock): undo
 * is Ctrl+Z on the editor, run by CodeMirror's own history keymap.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { EditorView } from '@codemirror/view'
import { renderWithProviders } from '../../../test/utils'

// jsdom has no layout: CodeMirror measures text ranges.
const emptyRects = () => Object.assign([], { item: () => null }) as unknown as DOMRectList
Range.prototype.getClientRects = emptyRects
Range.prototype.getBoundingClientRect = () => new DOMRect()

vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

let disk = { content: '', maskedValues: 0 }
const mockGetCompose = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: (...args: unknown[]) => mockGetCompose(...args),
    updateCompose: vi.fn(),
    lintCompose: vi.fn(),
    get: vi.fn(),
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
  gitApi: { pull: vi.fn() },
  authApi: { unlockEnv: vi.fn() },
}))

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() } }))

import { ComposeEditor } from '../ComposeEditor'

const REAL = 'services:\n  db:\n    environment:\n      - POSTGRES_PASSWORD=hunter2\n'
const MASKED = 'services:\n  db:\n    environment:\n      - POSTGRES_PASSWORD=***\n'

function renderEditor() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const { container } = renderWithProviders(<ComposeEditor stackId="s1" />, { queryClient })
  return { queryClient, container }
}

function liveView(container: HTMLElement): EditorView {
  const el = container.querySelector('.cm-editor')
  if (!(el instanceof HTMLElement)) throw new Error('no CodeMirror editor rendered')
  const view = EditorView.findFromDOM(el)
  if (!view) throw new Error('no EditorView for the editor')
  return view
}

// A minute passes before each server response, as it does between opening a
// file, unlocking and the unlock window closing. Without it CodeMirror's
// 500ms grouping merges the replacements into one undo step and hides them.
function laterByAMinute() {
  vi.setSystemTime(Date.now() + 60_000)
}

async function serve(queryClient: QueryClient, next: typeof disk) {
  laterByAMinute()
  disk = next
  const calls = mockGetCompose.mock.calls.length
  await act(() => queryClient.invalidateQueries({ queryKey: ['stack', 's1', 'compose'] }))
  await waitFor(() => expect(mockGetCompose.mock.calls.length).toBeGreaterThan(calls))
}

function ctrlZ(view: EditorView) {
  act(() => {
    fireEvent.keyDown(view.contentDOM, { key: 'z', code: 'KeyZ', keyCode: 90, ctrlKey: true })
  })
}

/**
 * Positive control first: a user edit, which the first Ctrl+Z must undo (so
 * the key really reaches CodeMirror's history). Then Ctrl+Z as far as the
 * history goes; returns the text after each further press.
 */
function editThenUndoAll(view: EditorView): string[] {
  act(() => {
    laterByAMinute()
    view.dispatch({ changes: { from: 0, insert: '# edit\n' }, userEvent: 'input.type' })
  })
  expect(view.state.doc.toString()).toBe('# edit\n' + REAL)
  ctrlZ(view)
  expect(view.state.doc.toString()).toBe(REAL)
  const seen: string[] = []
  for (let i = 0; i < 10; i++) {
    ctrlZ(view)
    seen.push(view.state.doc.toString())
  }
  return seen
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.clearAllMocks()
  mockGetCompose.mockImplementation(() => Promise.resolve({ ...disk, locked: disk.maskedValues > 0 }))
})

afterEach(() => {
  vi.useRealTimers()
})

describe('ComposeEditor undo across a server replacement (agent-os-w6wx)', () => {
  it('opened masked, then unlocked: undo never brings the masked text back', async () => {
    disk = { content: MASKED, maskedValues: 1 }
    const { queryClient, container } = renderEditor()
    await waitFor(() => expect(liveView(container).state.doc.toString()).toBe(MASKED))

    await serve(queryClient, { content: REAL, maskedValues: 0 }) // unlock
    const view = liveView(container)
    await waitFor(() => expect(view.state.doc.toString()).toBe(REAL))
    await waitFor(() => expect(view.state.readOnly).toBe(false))

    for (const text of editThenUndoAll(view)) {
      expect(text).not.toContain('***')
    }
    expect(view.state.doc.toString()).toBe(REAL)
  })

  it('a clean editor re-locked after the unlock window, then unlocked: undo never brings the masked text back', async () => {
    disk = { content: REAL, maskedValues: 0 }
    const { queryClient, container } = renderEditor()
    await waitFor(() => expect(liveView(container).state.doc.toString()).toBe(REAL))

    await serve(queryClient, { content: MASKED, maskedValues: 1 }) // re-lock
    await waitFor(() => expect(liveView(container).state.doc.toString()).toBe(MASKED))
    await serve(queryClient, { content: REAL, maskedValues: 0 }) // unlock again
    const view = liveView(container)
    await waitFor(() => expect(view.state.doc.toString()).toBe(REAL))
    await waitFor(() => expect(view.state.readOnly).toBe(false))

    for (const text of editThenUndoAll(view)) {
      expect(text).not.toContain('***')
    }
    expect(view.state.doc.toString()).toBe(REAL)
  })
})
