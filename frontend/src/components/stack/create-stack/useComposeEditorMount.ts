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

  useCodeMirrorEditor(editorRef, {
    doc: pendingCompose ?? composeContent,
    // Every edit is forwarded: Create submits composeContent, so dropping a
    // change that lands in the same frame as the previous one loses it. No echo
    // loop needs breaking here: useCodeMirrorEditor only pushes `doc` into the
    // view when it differs from the view's text, and an edit makes them equal.
    onChange: setComposeContent,
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
