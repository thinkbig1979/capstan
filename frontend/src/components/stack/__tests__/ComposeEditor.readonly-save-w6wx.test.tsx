/**
 * agent-os-w6wx review (cor C4): Ctrl+S and the Save button still sent a save
 * while the editor was read-only over masked secrets; only the server's 403
 * stopped it. Both are now no-ops while read-only.
 *
 * useCodeMirrorEditor is mocked as in ComposeEditor.locked-w6wx.test.tsx.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
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

describe('ComposeEditor save while read-only (agent-os-w6wx)', () => {
  it('Ctrl+S sends nothing while the editor is read-only', async () => {
    disk = { content: MASKED, maskedValues: 1 }
    renderEditor()
    await waitFor(() => expect(view.text).toBe(MASKED))
    await waitFor(() => expect(lastReadOnly).toBe(true))

    act(() => {
      capturedOnSave?.()
    })
    await new Promise((r) => setTimeout(r, 50))
    expect(mockLintCompose).not.toHaveBeenCalled()
    expect(mockUpdateCompose).not.toHaveBeenCalled()
  })

  it('the Save button is disabled and sends nothing while read-only, even with a change in the view', async () => {
    disk = { content: MASKED, maskedValues: 1 }
    renderEditor()
    await waitFor(() => expect(view.text).toBe(MASKED))
    await waitFor(() => expect(capturedOnChange).toBeDefined())
    // A change reaching the view while it is read-only (the editor reports it
    // through onChange) must still not make Save send masked text.
    act(() => {
      view.text = MASKED + '# x\n'
      capturedOnChange?.(MASKED + '# x\n')
    })
    const save = screen.getByRole('button', { name: /^Save$/ })
    expect(save).toBeDisabled()
    fireEvent.click(save)
    await new Promise((r) => setTimeout(r, 50))
    expect(mockLintCompose).not.toHaveBeenCalled()
    expect(mockUpdateCompose).not.toHaveBeenCalled()
  })

  it('control: an editable file with a change saves (the instrument can see a save)', async () => {
    disk = { content: REAL, maskedValues: 0 }
    mockUpdateCompose.mockResolvedValue({ saved: true, lintResults: [] })
    renderEditor()
    await waitFor(() => expect(view.text).toBe(REAL))
    await waitFor(() => expect(capturedOnChange).toBeDefined())
    act(() => {
      view.text = EDITED
      capturedOnChange?.(EDITED)
    })
    act(() => {
      capturedOnSave?.()
    })
    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalledTimes(1))
  })
})
