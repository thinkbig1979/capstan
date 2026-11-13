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
import { describe, it, expect, vi, beforeEach } from 'vitest'
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
let lastReadOnly: boolean | undefined

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn(
    (_ref: unknown, opts: { doc: string; readOnly?: boolean; onChange?: (c: string) => void; onSave?: () => boolean }) => {
      const viewRef = useRef<unknown>(null)
      viewRef.current = {
        state: { doc: { toString: () => view.text }, selection: { main: { from: 0, to: 0 } } },
        dispatch: mockDispatch,
        destroy: vi.fn(),
      }
      capturedOnChange = opts.onChange
      capturedOnSave = opts.onSave
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
  lastReadOnly = undefined
  mockGetCompose.mockImplementation(() => Promise.resolve({ ...disk, locked: disk.maskedValues > 0 }))
  mockLintCompose.mockResolvedValue({ valid: true, lintResults: [] })
})

describe('ComposeEditor with masked secrets (agent-os-w6wx)', () => {
  it('is read-only with an unlock banner while values are masked, and editable without', async () => {
    disk = { content: MASKED, maskedValues: 1 }
    renderEditor()

    await waitFor(() => expect(view.text).toBe(MASKED))
    const banner = await screen.findByRole('status')
    expect(banner).toHaveTextContent('Secrets in this file are hidden, so it is read-only')
    expect(screen.getByRole('button', { name: 'Unlock to edit' })).toBeInTheDocument()
    expect(lastReadOnly).toBe(true)
    expect(screen.getByRole('button', { name: /Extract to \.env/ })).toBeDisabled()
  })

  it('a file with nothing masked is editable and shows no banner', async () => {
    disk = { content: 'services: {}\n', maskedValues: 0 }
    renderEditor()

    await waitFor(() => expect(view.text).toBe('services: {}\n'))
    expect(screen.queryByRole('button', { name: 'Unlock to edit' })).not.toBeInTheDocument()
    expect(lastReadOnly).toBe(false)
  })

  it('a re-lock keeps unsaved edits, raises no false notice, and the refused save says why', async () => {
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

    mockUpdateCompose.mockRejectedValue({
      status: 403,
      code: 'FORBIDDEN',
      message: 'Re-enter your password to edit a compose file that holds secrets',
    })
    act(() => {
      capturedOnSave?.()
    })
    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(toastMocks.error).toHaveBeenCalled())
    expect(JSON.stringify(toastMocks.error.mock.calls)).toContain('Re-enter your password')
    expect(view.text).toBe(EDITED)
  })
})
