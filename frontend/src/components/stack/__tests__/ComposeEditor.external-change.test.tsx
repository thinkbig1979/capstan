/**
 * agent-os-umig: a git pull rewrites the compose file on disk. The open editor
 * must show the pulled file when it holds no edits, and must keep unsaved
 * edits (with a notice and a Reload) when it does. Before the fix the pull
 * never refetched the compose query, so the editor stayed on the old text and
 * the next save wrote it over the pulled change.
 *
 * useCodeMirrorEditor is mocked, as in the other ComposeEditor tests. The mock
 * keeps one mutable view text and copies a changed `doc` into it, the way the
 * real hook's doc effect does (hooks/useCodeMirrorEditor.ts, the
 * `[options.doc]` effect). `view.text` is therefore what the user sees.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { renderWithProviders } from '../../../test/utils'

const view = { text: '' }
const mockDispatch = vi.fn((tr: { changes: { from: number; to: number; insert: string } }) => {
  const { from, to, insert } = tr.changes
  view.text = view.text.slice(0, from) + insert + view.text.slice(to)
})
let capturedOnChange: ((content: string) => void) | undefined

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn((_ref: unknown, opts: { doc: string; onChange?: (c: string) => void }) => {
    const viewRef = useRef<unknown>(null)
    viewRef.current = {
      state: { doc: { toString: () => view.text }, selection: { main: { from: 0, to: 0 } } },
      dispatch: mockDispatch,
      destroy: vi.fn(),
    }
    capturedOnChange = opts.onChange
    useEffect(() => {
      if (opts.doc !== view.text) view.text = opts.doc
    }, [opts.doc])
    return { viewRef, isDark: false }
  }),
}))

vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

let diskText = ''
const mockGetCompose = vi.fn()
const mockUpdateCompose = vi.fn()
const mockLintCompose = vi.fn()
const mockGitPull = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: (...args: unknown[]) => mockGetCompose(...args),
    updateCompose: (...args: unknown[]) => mockUpdateCompose(...args),
    lintCompose: (...args: unknown[]) => mockLintCompose(...args),
    get: vi.fn(),
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
  gitApi: {
    pull: (...args: unknown[]) => mockGitPull(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ComposeEditor } from '../ComposeEditor'
import { useGitPull } from '@/hooks/useGit'

const NOTICE = 'This file changed on disk. Reload to see the new version; saving now overwrites it.'
const ORIGINAL = 'services:\n  web:\n    image: nginx:1.24\n'
const PULLED = 'services:\n  web:\n    image: nginx:1.27\n'
const EDITED = 'services:\n  web:\n    image: nginx:1.25\n'

// The Pull button lives in the stack header beside the editor tab
// (StackPage.tsx renders GitStatus there), so both share one query client.
function EditorWithPull() {
  const pull = useGitPull()
  return (
    <>
      <button type="button" onClick={() => pull.mutate({ stackId: 's1' })}>
        Pull now
      </button>
      <ComposeEditor stackId="s1" />
    </>
  )
}

function renderEditor() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  renderWithProviders(<EditorWithPull />, { queryClient })
  return queryClient
}

async function loaded() {
  await waitFor(() => expect(view.text).toBe(ORIGINAL))
  await waitFor(() => expect(capturedOnChange).toBeDefined())
}

function type(text: string) {
  act(() => {
    view.text = text
    capturedOnChange?.(text)
  })
}

async function pullTo(text: string) {
  diskText = text
  const calls = mockGetCompose.mock.calls.length
  fireEvent.click(screen.getByText('Pull now'))
  await waitFor(() => expect(mockGetCompose.mock.calls.length).toBeGreaterThan(calls))
}

beforeEach(() => {
  vi.clearAllMocks()
  view.text = ''
  capturedOnChange = undefined
  diskText = ORIGINAL
  mockGetCompose.mockImplementation(() => Promise.resolve({ content: diskText }))
  mockLintCompose.mockResolvedValue({ lintResults: [] })
  mockUpdateCompose.mockImplementation((_id: string, content: string) => {
    diskText = content
    return Promise.resolve({ lintResults: [] })
  })
  mockGitPull.mockResolvedValue({ outcome: 'success', reason: 'Pulled' })
})

describe('ComposeEditor after a git pull (agent-os-umig)', () => {
  it('a clean editor shows the pulled file', async () => {
    renderEditor()
    await loaded()

    await pullTo(PULLED)

    await waitFor(() => expect(view.text).toBe(PULLED))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(screen.getByText('Save')).toBeDisabled()
  })

  it('unsaved edits stay, the notice shows, and Reload loads the pulled file', async () => {
    renderEditor()
    await loaded()
    type(EDITED)

    await pullTo(PULLED)

    expect(await screen.findByText(NOTICE)).toBeInTheDocument()
    expect(view.text).toBe(EDITED)
    expect(screen.getByText('Save')).not.toBeDisabled()

    fireEvent.click(screen.getByText('Reload'))

    await waitFor(() => expect(view.text).toBe(PULLED))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(screen.getByText('Save')).toBeDisabled()
  })

  it('a second change while the notice is up: Reload loads the newest file', async () => {
    renderEditor()
    await loaded()
    type(EDITED)

    await pullTo(PULLED)
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()
    const newest = 'services:\n  web:\n    image: nginx:1.28\n'
    await pullTo(newest)
    await waitFor(() => expect(mockGetCompose).toHaveBeenCalledTimes(3))

    fireEvent.click(screen.getByText('Reload'))

    await waitFor(() => expect(view.text).toBe(newest))
  })

  it('no notice when the pulled file equals what the editor already shows', async () => {
    renderEditor()
    await loaded()
    type(PULLED)

    await pullTo(PULLED)

    // The editor now matches disk: nothing to overwrite, so not dirty either.
    await waitFor(() => expect(screen.getByText('Save')).toBeDisabled())
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(view.text).toBe(PULLED)
  })

  it('no notice and edits kept when the file on disk did not change', async () => {
    renderEditor()
    await loaded()
    type(EDITED)

    await pullTo(ORIGINAL)

    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(view.text).toBe(EDITED)
    expect(screen.getByText('Save')).not.toBeDisabled()
  })

  it('a successful save clears the notice; the saved text is the new baseline', async () => {
    renderEditor()
    await loaded()
    type(EDITED)
    await pullTo(PULLED)
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalledWith('s1', EDITED))
    await waitFor(() => expect(screen.queryByText(NOTICE)).not.toBeInTheDocument())
    expect(view.text).toBe(EDITED)
    await waitFor(() => expect(screen.getByText('Save')).toBeDisabled())
  })

  it('a failed save keeps the notice', async () => {
    mockUpdateCompose.mockRejectedValue({ status: 500, message: 'disk full' })
    renderEditor()
    await loaded()
    type(EDITED)
    await pullTo(PULLED)
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()

    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByText('Save')).not.toBeDisabled())
    expect(screen.getByText(NOTICE)).toBeInTheDocument()
    expect(view.text).toBe(EDITED)
  })

  it("typing after a save is kept when the save's own refetch lands", async () => {
    const queryClient = renderEditor()
    await loaded()
    type(EDITED)

    // Hold the refetch the save triggers until the user has typed more.
    let release: () => void = () => {}
    mockGetCompose.mockImplementationOnce(
      () => new Promise((resolve) => {
        release = () => resolve({ content: diskText })
      }),
    )
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalledWith('s1', EDITED))
    await waitFor(() => expect(mockGetCompose).toHaveBeenCalledTimes(2))

    const more = EDITED + '    restart: always\n'
    type(more)
    await act(async () => release())
    // Asserting straight after the release passes before the refetch has
    // reached the editor (observed: green at base). Wait for it to land.
    const key = ['stack', 's1', 'compose']
    await waitFor(() => expect(queryClient.getQueryState(key)?.fetchStatus).toBe('idle'))
    expect(queryClient.getQueryData(key)).toEqual({ text: EDITED, masked: false })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })

    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(view.text).toBe(more)
    expect(screen.getByText('Save')).not.toBeDisabled()
  })
  // The sync effect in ComposeEditor.tsx: the view must follow a load whose
  // text equals the previous load. Reached only when the save's own refetch
  // lands while the user is still typing (so nothing is loaded and `loaded`
  // stays at A), the user types back to the saved text, and then the file on
  // disk goes back to A. The doc prop does not change (A -> A), so only that
  // effect can put A in the view.
  it('the view shows the file when a reload brings back the text loaded before the save', async () => {
    const queryClient = renderEditor()
    await loaded()
    type(EDITED)

    let release: () => void = () => {}
    mockGetCompose.mockImplementationOnce(
      () => new Promise((resolve) => {
        release = () => resolve({ content: diskText })
      }),
    )
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalledWith('s1', EDITED))
    await waitFor(() => expect(mockGetCompose).toHaveBeenCalledTimes(2))
    type(EDITED + '    restart: always\n')
    await act(async () => release())
    const key = ['stack', 's1', 'compose']
    await waitFor(() => expect(queryClient.getQueryState(key)?.fetchStatus).toBe('idle'))
    expect(queryClient.getQueryData(key)).toEqual({ text: EDITED, masked: false })

    // Back to the saved text: clean again, view shows EDITED.
    type(EDITED)
    await waitFor(() => expect(screen.getByText('Save')).toBeDisabled())

    await pullTo(ORIGINAL)

    await waitFor(() => expect(queryClient.getQueryData(key)).toEqual({ text: ORIGINAL, masked: false }))
    await waitFor(() => expect(view.text).toBe(ORIGINAL))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
  })
})
