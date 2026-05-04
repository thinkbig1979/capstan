import { useEffect, useRef, useMemo } from 'react'
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
  onSaveRef.current = options.onSave
  onChangeRef.current = options.onChange
  onSelectRef.current = options.onSelect

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

  return { viewRef, isDark }
}
