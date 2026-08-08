import { useEffect, useRef, useMemo } from 'react'
// The aggregate `codemirror` package is kept deliberately, not by omission
// (agent-os-bhyn considered dropping it in favour of the eight individual
// @codemirror/* packages this file's neighbours already import).
//
// EditorView is also exported by @codemirror/view, but `basicSetup` exists only
// here and is 18 curated extensions plus a keymap composed from seven of them.
// Inlining it would mean owning that list and tracking upstream changes to it by
// hand, and it would ADD @codemirror/commands and @codemirror/language as direct
// dependencies — a net increase, not a reduction.
//
// It also would not close the version-split hazard it looks like it should.
// Measured 2026-08-08: six installed packages depend on @codemirror/language —
// this aggregate plus autocomplete, commands, lang-json, lang-yaml and
// theme-one-dark — so dropping the aggregate removes one of six participants and
// leaves the failure mode (agent-os-l1hn: two @codemirror/language copies, facet
// identity mismatch, YAML silently unhighlighted) fully reachable. The control for
// that is hooks/__tests__/codemirror-yaml-highlighting.test.ts, which mocks
// nothing on purpose, plus the family-declared-together convention recorded in
// knip.jsonc's ignoreDependencies entry.
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { oneDark } from '@codemirror/theme-one-dark'
import { keymap } from '@codemirror/view'
import { search } from '@codemirror/search'
import { autocompletion } from '@codemirror/autocomplete'
import { useUIStore } from '@/stores/uiStore'

interface UseCodeMirrorEditorOptions {
  doc: string
  onSave?: () => boolean
  onChange?: (content: string) => void
  onSelect?: (text: string) => void
  deps?: React.DependencyList
}

export function useCodeMirrorEditor(
  containerRef: React.RefObject<HTMLDivElement | null>,
  options: UseCodeMirrorEditorOptions,
) {
  const viewRef = useRef<EditorView | null>(null)
  const { theme } = useUIStore()
  const onSaveRef = useRef(options.onSave)
  const onChangeRef = useRef(options.onChange)
  const onSelectRef = useRef(options.onSelect)

  useEffect(() => {
    onSaveRef.current = options.onSave
    onChangeRef.current = options.onChange
    onSelectRef.current = options.onSelect
  })

  const isDark = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' &&
        typeof window !== 'undefined' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  useEffect(() => {
    if (!containerRef.current) return

    const extensions = [
      basicSetup,
      yaml(),
      keymap.of([
        {
          key: 'Mod-s',
          run: () => onSaveRef.current?.() ?? true,
        },
      ]),
      search({ top: true }),
      autocompletion(),
      EditorView.theme({
        '&': { fontSize: '14px' },
        '.cm-scroller': {
          fontFamily:
            'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
        },
      }),
    ]

    if (isDark) {
      extensions.push(oneDark)
    }

    if (onChangeRef.current || onSelectRef.current) {
      extensions.push(
        EditorView.updateListener.of((update) => {
          if (update.docChanged && onChangeRef.current) {
            onChangeRef.current(update.state.doc.toString())
          }
          if (update.selectionSet || update.docChanged) {
            const sel = update.state.selection.main
            if (onSelectRef.current) {
              onSelectRef.current(
                sel.from !== sel.to
                  ? update.state.sliceDoc(sel.from, sel.to)
                  : '',
              )
            }
          }
        }),
      )
    }

    const state = EditorState.create({
      doc: options.doc,
      extensions,
    })

    const view = new EditorView({
      state,
      parent: containerRef.current,
    })

    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
    }
  }, [isDark, ...(options.deps || [])]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const currentDoc = view.state.doc.toString()
    if (options.doc !== currentDoc) {
      view.dispatch({
        changes: { from: 0, to: currentDoc.length, insert: options.doc },
      })
    }
  }, [options.doc])

  return { viewRef, isDark }
}
