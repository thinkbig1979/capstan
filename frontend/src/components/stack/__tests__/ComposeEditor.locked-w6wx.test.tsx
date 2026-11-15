/**
 * agent-os-w6wx (Edwin, 2026-11-13): a compose file whose secrets the server
 * masked for a locked session is read-only, with an unlock banner. A re-lock
 * while the editor holds unsaved unlocked edits keeps them, raises no
 * changed-on-disk notice, never loads "***" over them, and a save is refused
 * by the server (403) with its message shown.
 *
 * useCodeMirrorEditor is mocked as in the other ComposeEditor tests; the mock
 * records the readOnly option the editor passes it.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { act, screen, waitFor } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { renderWithProviders } from '../../../test/utils'

const view = { text: '' }
const mockDispatch = vi.fn((tr: { changes: { from: number; to: number; insert: string } }) => {
  const { from, to, insert } = tr.changes
  view.text = view.text.slice(0, from) + insert + view.text.slice(to)
})
let capturedOnChange: ((content: string) => void) | undefined
let capturedOnSave: (() => boolean) | undefined
let capturedOnSelect: ((text: string) => void) | undefined
let lastReadOnly: boolean | undefined

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn(
    (
      _ref: unknown,
      opts: {
        doc: string
        readOnly?: boolean
        onChange?: (c: string) => void
        onSave?: () => boolean
        onSelect?: (text: string) => void
      },
    ) => {
      const viewRef = useRef<unknown>(null)
      viewRef.current = {
        state: { doc: { toString: () => view.text }, selection: { main: { from: 0, to: 0 } } },
        dispatch: mockDispatch,
        destroy: vi.fn(),
      }
      capturedOnChange = opts.onChange
      capturedOnSave = opts.onSave
      capturedOnSelect = opts.onSelect
      lastReadOnly = opts.readOnly
      useEffect(() => {
        if (opts.doc !== view.text) view.text = opts.doc
      }, [opts.doc])
      return { viewRef, isDark: false }
    },
  ),
}))

vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

let disk = { content: '', maskedValues: 0 }
const mockGetCompose = vi.fn()
const mockUpdateCompose = vi.fn()
const mockLintCompose = vi.fn()

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
  gitApi: { pull: vi.fn() },
  authApi: { unlockEnv: vi.fn() },
}))

const toastMocks = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }))
vi.mock('sonner', () => ({ toast: toastMocks }))

import { ComposeEditor } from '../ComposeEditor'
import { LOCKED_COMPOSE_NOTICE } from '@/lib/locked-copy'

// The server answers locked exactly when the session holds no unlock token, so
// the unlock store moves with every payload the mock serves, before it lands
// (agent-os-xz6h): once a refetch has landed, an unlocked payload under a
// locked store cannot happen. Between a lock and its refetch it can, and the
// xz6h tests drive that window directly.
function storeFollows<T extends object>(payload: T): T {
  const locked = 'locked' in payload && payload.locked === true
  const store = useEnvUnlockStore.getState()
  if (locked && store.unlockedUntil !== null) store.lock()
  else if (!locked && store.unlockedUntil === null) store.unlock('tok')
  return payload
}

afterEach(() => {
  useEnvUnlockStore.getState().lock()
})

const NOTICE = 'This file changed on disk. Reload to see the new version; saving now overwrites it.'
const REAL = 'services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: hunter2\n'
const MASKED = 'services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: ***\n'
const EDITED = 'services:\n  db:\n    image: postgres:17\n    environment:\n      POSTGRES_PASSWORD: hunter2\n'

function renderEditor() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  renderWithProviders(<ComposeEditor stackId="s1" />, { queryClient })
  return queryClient
}

async function refetch(queryClient: QueryClient) {
  const calls = mockGetCompose.mock.calls.length
  await act(() => queryClient.invalidateQueries({ queryKey: ['stack', 's1', 'compose'] }))
  await waitFor(() => expect(mockGetCompose.mock.calls.length).toBeGreaterThan(calls))
}

beforeEach(() => {
  vi.clearAllMocks()
  view.text = ''
  capturedOnChange = undefined
  capturedOnSave = undefined
  capturedOnSelect = undefined
  lastReadOnly = undefined
  mockGetCompose.mockImplementation(() => Promise.resolve(storeFollows({ ...disk, locked: disk.maskedValues > 0 })))
  mockLintCompose.mockResolvedValue({ valid: true, lintResults: [] })
})

describe('ComposeEditor with masked secrets (agent-os-w6wx)', () => {
  it('is read-only with an unlock banner while values are masked, and editable without', async () => {
    disk = { content: MASKED, maskedValues: 1 }
    renderEditor()

    await waitFor(() => expect(view.text).toBe(MASKED))
    const banner = await screen.findByRole('status')
    // What a locked session can and cannot do (agent-os-ly6i).
    expect(banner).toHaveTextContent(LOCKED_COMPOSE_NOTICE)
    expect(screen.getByRole('button', { name: 'Unlock to edit' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Lint/ })).toBeDisabled()
    expect(lastReadOnly).toBe(true)
    expect(screen.getByRole('button', { name: /Extract to \.env/ })).toBeDisabled()
  })

  // Review F4 (agent-os-ly6i): locked, a file with nothing masked still
  // cannot extract to .env: the extract is a compose-env save, which the
  // server refuses while locked.
  it('locked with nothing masked: Extract to .env is disabled', async () => {
    disk = { content: 'services:\n  web:\n    image: nginx\n', maskedValues: 0 }
    mockGetCompose.mockImplementation(() => Promise.resolve(storeFollows({ ...disk, locked: true })))
    renderEditor()

    await waitFor(() => expect(view.text).toBe(disk.content))
    act(() => {
      capturedOnSelect?.('nginx')
    })
    const extract = screen.getByRole('button', { name: /Extract to \.env/ })
    expect(extract).toBeDisabled()
    expect(extract).toHaveAttribute('title', 'Unlock to extract values to .env')
  })

  it('a file with nothing masked is editable and shows no banner', async () => {
    disk = { content: 'services: {}\n', maskedValues: 0 }
    renderEditor()

    await waitFor(() => expect(view.text).toBe('services: {}\n'))
    expect(screen.queryByRole('button', { name: 'Unlock to edit' })).not.toBeInTheDocument()
    expect(lastReadOnly).toBe(false)
  })

  it('a re-lock keeps unsaved edits, raises no false notice, and sends no save or lint', async () => {
    disk = { content: REAL, maskedValues: 0 }
    const queryClient = renderEditor()
    await waitFor(() => expect(view.text).toBe(REAL))
    await waitFor(() => expect(capturedOnChange).toBeDefined())
    act(() => {
      view.text = EDITED
      capturedOnChange?.(EDITED)
    })

    // The unlock window closes: the refetch brings the masked file.
    disk = { content: MASKED, maskedValues: 1 }
    await refetch(queryClient)

    expect(await screen.findByText('Values locked again; unlock to save your edits.')).toBeInTheDocument()
    expect(view.text).toBe(EDITED)
    expect(screen.queryByText(NOTICE)).not.toBeInTheDocument()
    expect(lastReadOnly).toBe(false)

    // Locked, the server refuses save and lint (agent-os-ly6i): Ctrl+S and
    // the buttons send nothing, and the edits stay.
    act(() => {
      capturedOnSave?.()
    })
    expect(screen.getByRole('button', { name: /Save/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Lint/ })).toBeDisabled()
    expect(mockLintCompose).not.toHaveBeenCalled()
    expect(mockUpdateCompose).not.toHaveBeenCalled()
    expect(view.text).toBe(EDITED)
  })
})
