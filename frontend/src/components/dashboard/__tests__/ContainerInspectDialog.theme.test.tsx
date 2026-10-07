/**
 * agent-os-z91e.45: a theme change must reconfigure the live Inspect viewer,
 * not rebuild it. ContainerInspectDialog listed `isDark` in the deps of the
 * effect that creates its EditorView, so the theme toggle destroyed the view
 * and built a new one: the scroll position and selection reset.
 *
 * No CodeMirror module is mocked: the assertions read the real EditorView's
 * identity, selection and dark-theme facet. Only the API layer is faked.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, render, waitFor } from '@testing-library/react'
import { EditorView } from 'codemirror'
import { useUIStore } from '@/stores/uiStore'

const mockInspect = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    inspectContainer: (...args: unknown[]) => mockInspect(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

import { ContainerInspectDialog } from '../ContainerInspectDialog'

// The dialog renders into a Radix portal, so look in the whole document.
function liveView(): EditorView {
  const dom = document.querySelector('.cm-editor')
  if (!dom) throw new Error('no .cm-editor in the document: the viewer was not created')
  const view = EditorView.findFromDOM(dom as HTMLElement)
  if (!view) throw new Error('.cm-editor has no EditorView behind it')
  return view
}

describe('Inspect viewer keeps its view on a theme change (agent-os-z91e.45)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockInspect.mockResolvedValue({ Id: 'c1', State: { Status: 'running' } })
    act(() => useUIStore.getState().setTheme('light'))
  })

  afterEach(() => {
    act(() => useUIStore.getState().setTheme('light'))
  })

  it('reuses the same EditorView, document and selection when the theme flips', async () => {
    render(
      <ContainerInspectDialog containerId="c1abcdef123456" containerName="web" open onOpenChange={() => {}} />,
    )

    await waitFor(() => expect(liveView().state.doc.length).toBeGreaterThan(0))
    const before = liveView()
    const doc = before.state.doc.toString()
    act(() => before.dispatch({ selection: { anchor: 2, head: 6 } }))
    expect(before.state.facet(EditorView.darkTheme)).toBe(false)

    act(() => useUIStore.getState().setTheme('dark'))

    const after = liveView()
    // The user-visible loss: a rebuilt view starts with no selection and at the top.
    expect(after).toBe(before)
    expect(after.state.doc.toString()).toBe(doc)
    expect(after.state.selection.main.from).toBe(2)
    expect(after.state.selection.main.to).toBe(6)
    // And the theme did change, so the test cannot pass by ignoring it.
    expect(after.state.facet(EditorView.darkTheme)).toBe(true)

    act(() => useUIStore.getState().setTheme('light'))
    expect(liveView()).toBe(before)
    expect(before.state.facet(EditorView.darkTheme)).toBe(false)
  })

  it('opens dark when the theme is already dark', async () => {
    act(() => useUIStore.getState().setTheme('dark'))
    render(
      <ContainerInspectDialog containerId="c1abcdef123456" containerName="web" open onOpenChange={() => {}} />,
    )
    await waitFor(() => expect(liveView().state.doc.length).toBeGreaterThan(0))
    expect(liveView().state.facet(EditorView.darkTheme)).toBe(true)
  })
})
