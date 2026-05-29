import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useRef } from 'react'

const dispatchMock = vi.fn()
let createdDocs: string[] = []

vi.mock('codemirror', () => {
  class MockEditorView {
    static theme = () => []
    static updateListener = { of: () => [] }
    _doc: string
    constructor(opts: { state: { doc: { toString: () => string } } }) {
      this._doc = opts.state.doc.toString()
      createdDocs.push(this._doc)
    }
    dispatch(args: { changes: { from: number; to: number; insert: string } }) {
      dispatchMock(args)
      if (args?.changes?.insert !== undefined) {
        this._doc = args.changes.insert
      }
    }
    destroy() {}
    get state() {
      const doc = this._doc
      return { doc: { toString: () => doc, length: doc.length } }
    }
  }
  return { basicSetup: [], EditorView: MockEditorView }
})

vi.mock('@codemirror/state', () => ({
  EditorState: {
    create: ({ doc }: { doc: string }) => ({
      doc: { toString: () => doc, length: doc.length },
    }),
  },
}))

vi.mock('@codemirror/view', () => ({
  EditorView: class {
    static theme = () => []
    static updateListener = { of: () => [] }
  },
  keymap: { of: () => [] },
}))

vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))

vi.mock('@/stores/uiStore', () => ({
  useUIStore: () => ({ theme: 'light' }),
}))

import { useCodeMirrorEditor } from '../useCodeMirrorEditor'

function useHarness(doc: string) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  if (containerRef.current === null) {
    containerRef.current = document.createElement('div')
  }
  return useCodeMirrorEditor(containerRef, { doc, deps: ['stack-1'] })
}

describe('useCodeMirrorEditor', () => {
  beforeEach(() => {
    dispatchMock.mockClear()
    createdDocs = []
  })

  it('reactively syncs doc into the editor when it changes after mount', () => {
    const { rerender } = renderHook(({ doc }) => useHarness(doc), {
      initialProps: { doc: '' },
    })

    expect(createdDocs).toEqual([''])
    expect(dispatchMock).not.toHaveBeenCalled()

    act(() => {
      rerender({ doc: 'services:\n  web:\n    image: nginx\n' })
    })

    expect(dispatchMock).toHaveBeenCalledWith({
      changes: {
        from: 0,
        to: 0,
        insert: 'services:\n  web:\n    image: nginx\n',
      },
    })
  })

  it('does not re-dispatch when doc prop is unchanged', () => {
    const { rerender } = renderHook(({ doc }) => useHarness(doc), {
      initialProps: { doc: 'initial' },
    })

    expect(createdDocs).toEqual(['initial'])
    dispatchMock.mockClear()

    act(() => {
      rerender({ doc: 'initial' })
    })

    expect(dispatchMock).not.toHaveBeenCalled()
  })
})
