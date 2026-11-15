/**
 * agent-os-wn5h items 2 and 3, on ComposeEditor.
 *
 * Item 2: a compose file emptied on disk. hydrate skipped every '' body (it
 * stood for an absent one), so a clean editor kept the old text as if it were
 * on disk, and Reload under the notice did nothing while dropping the notice.
 *
 * Item 3 (not fixed, pinned): the notice stays when the user types the editor
 * back to the saved text. It is still true (the file on disk differs from the
 * editor) and Reload still loads it.
 *
 * The harness is ComposeEditor.external-change.test.tsx's.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useAuthStore } from '@/stores/authStore'
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
  useCodeMirrorEditor: vi.fn((_ref: unknown, opts: { doc: string; docKey?: unknown; onChange?: (c: string) => void }) => {
    const viewRef = useRef<unknown>(null)
    viewRef.current = {
      state: { doc: { toString: () => view.text }, selection: { main: { from: 0, to: 0 } } },
      dispatch: mockDispatch,
      destroy: vi.fn(),
    }
    capturedOnChange = opts.onChange
    // As the real hook: a new doc, or a new load (docKey) of the same text,
    // replaces a view that has moved on.
    useEffect(() => {
      if (opts.doc !== view.text) view.text = opts.doc
    }, [opts.doc, opts.docKey])
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

// No unlock window in these tests: with auth on, a payload served unlocked
// while the unlock store is locked cannot happen, and the editor would hide it
// (agent-os-xz6h).
beforeEach(() => {
  useAuthStore.setState({ authDisabled: true })
})
afterEach(() => {
  useAuthStore.setState({ authDisabled: false })
})
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

const EMPTY = ''

describe('ComposeEditor: a compose file emptied on disk (agent-os-wn5h item 2)', () => {
  it('wn5h: a clean editor shows the emptied file', async () => {
    renderEditor()
    await loaded()

    await pullTo(EMPTY)

    await waitFor(() => expect(view.text).toBe(EMPTY))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(screen.getByText('Save')).toBeDisabled()
  })

  it('wn5h: Reload under the notice loads the emptied file', async () => {
    renderEditor()
    await loaded()
    type(EDITED)

    await pullTo(EMPTY)
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()
    expect(view.text).toBe(EDITED)

    fireEvent.click(screen.getByText('Reload'))

    await waitFor(() => expect(view.text).toBe(EMPTY))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(screen.getByText('Save')).toBeDisabled()
  })
})

describe('ComposeEditor: typing back to the saved text under the notice (agent-os-wn5h item 3, pinned)', () => {
  it('wn5h: the notice stays, Save is disabled, and Reload loads the file on disk', async () => {
    renderEditor()
    await loaded()
    type(EDITED)
    await pullTo(PULLED)
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()

    type(ORIGINAL)

    expect(screen.getByText(NOTICE)).toBeInTheDocument()
    expect(screen.getByText('Save')).toBeDisabled()
    fireEvent.click(screen.getByText('Reload'))
    await waitFor(() => expect(view.text).toBe(PULLED))
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
  })
})
