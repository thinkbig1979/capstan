/**
 * agent-os-z91e.44: every edit in the create-stack editor must reach
 * composeContent, which is what Create submits. useComposeEditorMount used to
 * drop any change that landed inside the animation frame of the previous one
 * (a flag set per change, cleared one requestAnimationFrame later), so the
 * created compose file could miss the last edit.
 *
 * No CodeMirror module is mocked: edits are real transactions on the real
 * EditorView, and the assertions read composeContent as the hook's caller sees it.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act, render, cleanup, waitFor } from '@testing-library/react'
import { useCallback, useEffect, useState } from 'react'
import { EditorView } from 'codemirror'
import { useUIStore } from '@/stores/uiStore'
import { useComposeEditorMount } from '../useComposeEditorMount'

const SAVED = 'services:\n  web:\n    image: nginx\n'
const FIRST = 'services:\n  web:\n    image: nginx:first\n'
const SECOND = 'services:\n  web:\n    image: nginx:second\n'

// What the harness shows the test, written from effects and event handlers.
// setterCalls counts calls into the setter the hook is given: an echo loop
// (view -> state -> view -> state) would keep raising it, so one call per real
// edit is the no-loop proof.
const probe = {
  content: '',
  setterCalls: 0,
  convert: (_compose: string) => {},
}

function Harness() {
  const [composeContent, setRawComposeContent] = useState(SAVED)
  const [pendingCompose, setPendingCompose] = useState<string | null>(null)
  const [editorEpoch, setEditorEpoch] = useState(0)
  const setComposeContent: typeof setRawComposeContent = useCallback((v) => {
    probe.setterCalls++
    setRawComposeContent(v)
  }, [])
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
  useEffect(() => {
    probe.content = composeContent
    probe.convert = setPendingCompose
  }, [composeContent])
  return <div ref={editorRef} />
}

function liveView(container: ParentNode): EditorView {
  const dom = container.querySelector('.cm-editor')
  if (!dom) throw new Error('no .cm-editor in the document: the editor was not created')
  const view = EditorView.findFromDOM(dom as HTMLElement)
  if (!view) throw new Error('.cm-editor has no EditorView behind it')
  return view
}

function replaceDoc(view: EditorView, text: string) {
  view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } })
}

const nextFrame = () =>
  act(async () => {
    await new Promise((r) => requestAnimationFrame(() => r(null)))
  })

async function mountSettled() {
  const { container } = render(<Harness />)
  await waitFor(() => expect(liveView(container).state.doc.toString()).toBe(SAVED))
  // Let the one-off editorEpoch bump rebuild the view before the test edits it.
  await nextFrame()
  await nextFrame()
  return { container, view: liveView(container) }
}

describe('create-stack editor forwards every edit (agent-os-z91e.44)', () => {
  // jsdom has no matchMedia, which the default 'system' theme reads.
  beforeEach(() => {
    probe.setterCalls = 0
    act(() => useUIStore.getState().setTheme('light'))
  })
  afterEach(() => cleanup())

  it('keeps the second of two edits made with no animation frame between them', async () => {
    const { container, view } = await mountSettled()

    act(() => replaceDoc(view, FIRST))
    act(() => replaceDoc(view, SECOND))

    expect(probe.content).toBe(SECOND)
    await nextFrame()
    expect(probe.content).toBe(SECOND)
    expect(liveView(container).state.doc.toString()).toBe(SECOND)
  })

  it('keeps the last of two edits batched into one React update', async () => {
    const { container, view } = await mountSettled()

    act(() => {
      replaceDoc(view, FIRST)
      replaceDoc(view, SECOND)
    })

    expect(probe.content).toBe(SECOND)
    await nextFrame()
    expect(probe.content).toBe(SECOND)
    // The dropped edit also made the doc-push effect rewrite the view back to
    // the stale state, so the user watched their last edit vanish.
    expect(liveView(container).state.doc.toString()).toBe(SECOND)
  })

  it('forwards each edit once: no echo between the view and the state', async () => {
    const { container, view } = await mountSettled()
    probe.setterCalls = 0

    act(() => replaceDoc(view, FIRST))
    act(() => replaceDoc(view, SECOND))
    await nextFrame()
    await nextFrame()

    expect(probe.setterCalls).toBe(2)
    expect(probe.content).toBe(SECOND)
    expect(liveView(container)).toBe(view)
    expect(view.state.doc.toString()).toBe(SECOND)
  })

  it('a converted docker run command reaches the editor and settles', async () => {
    const { container, view } = await mountSettled()
    const converted = 'services:\n  app:\n    image: redis\n'
    probe.setterCalls = 0

    act(() => probe.convert(converted))
    await nextFrame()
    await nextFrame()

    expect(probe.content).toBe(converted)
    expect(view.state.doc.toString()).toBe(converted)
    expect(liveView(container)).toBe(view)
    // One write from consuming pendingCompose; the view's own change event
    // echoes the same text, which React discards, so it cannot grow past two.
    expect(probe.setterCalls).toBeLessThanOrEqual(2)
  })
})
