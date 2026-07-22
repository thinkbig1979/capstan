import { useEffect, useRef } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'

interface UseComposeEditorMountArgs {
  open: boolean
  composeTab: 'editor' | 'docker-run'
  editorEpoch: number
  setEditorEpoch: Dispatch<SetStateAction<number>>
  composeContent: string
  setComposeContent: Dispatch<SetStateAction<string>>
  pendingCompose: string | null
  setPendingCompose: Dispatch<SetStateAction<string | null>>
}

/**
 * Mounts the CodeMirror editor and owns the render-time choreography that
 * keeps it in sync with the dialog's compose content. Must be called
 * unconditionally in the component body (no early return before it) — the
 * pendingCompose consumption below relies on running during the owning
 * component's own render pass.
 */
export function useComposeEditorMount({
  open,
  composeTab,
  editorEpoch,
  setEditorEpoch,
  composeContent,
  setComposeContent,
  pendingCompose,
  setPendingCompose,
}: UseComposeEditorMountArgs) {
  const editorRef = useRef<HTMLDivElement>(null)
  const isUpdatingFromEditor = useRef(false)

  useCodeMirrorEditor(editorRef, {
    doc: pendingCompose ?? composeContent,
    onChange: (newContent) => {
      if (!isUpdatingFromEditor.current) {
        isUpdatingFromEditor.current = true
        setComposeContent(newContent)
        requestAnimationFrame(() => {
          isUpdatingFromEditor.current = false
        })
      }
    },
    deps: [open, composeTab, editorEpoch],
  })

  useEffect(() => {
    if (!open) return
    const id = requestAnimationFrame(() => setEditorEpoch((e) => e + 1))
    return () => cancelAnimationFrame(id)
  }, [open, setEditorEpoch])

  // Consume a pending compose conversion into the editable content. Adjusted
  // during render (rather than in an effect) — clearing `pendingCompose` in
  // the same pass makes this a one-shot assignment, not an unbounded loop.
  if (pendingCompose) {
    setComposeContent(pendingCompose)
    setPendingCompose(null)
  }

  return { editorRef }
}
