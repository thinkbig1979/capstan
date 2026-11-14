/**
 * agent-os-xz6h (probe, the compose twin of agent-os-wm7t): the compose editor
 * takes `masked` from the last payload alone. After a lock that payload is still
 * the unmasked one until the lock's refetch lands, and for good if it fails.
 * Does the unmasked compose text stay on screen?
 *
 * Setup copied from ComposeEditor.locked-w6wx.test.tsx (useCodeMirrorEditor is
 * mocked; `view.text` is what the editor shows).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, waitFor } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'

const view = { text: '' }
const mockDispatch = vi.fn((tr: { changes: { from: number; to: number; insert: string } }) => {
  const { from, to, insert } = tr.changes
  view.text = view.text.slice(0, from) + insert + view.text.slice(to)
})

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn(
    (_ref: unknown, opts: { doc: string }) => {
      const viewRef = useRef<unknown>(null)
      viewRef.current = {
        state: { doc: { toString: () => view.text }, selection: { main: { from: 0, to: 0 } } },
        dispatch: mockDispatch,
        destroy: vi.fn(),
      }
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

const REAL = 'services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: hunter2\n'
const MASKED = 'services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: ***\n'

function renderEditor() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  renderWithProviders(<ComposeEditor stackId="s1" />, { queryClient })
  return queryClient
}

beforeEach(() => {
  vi.clearAllMocks()
  view.text = ''
  mockGetCompose.mockImplementation(() => Promise.resolve({ ...disk, locked: disk.maskedValues > 0 }))
  mockLintCompose.mockResolvedValue({ valid: true, lintResults: [] })
})

describe('ComposeEditor after a lock (agent-os-xz6h probe)', () => {
  beforeEach(() => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    useAuthStore.setState({ authDisabled: false })
    disk = { content: REAL, maskedValues: 0 }
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('clean editor, lock, the refetch fails: the secret is not on screen', async () => {
    const queryClient = renderEditor()
    await waitFor(() => expect(view.text).toBe(REAL))
    mockGetCompose.mockRejectedValue(new Error('offline'))

    act(() => useEnvUnlockStore.getState().lock())
    const calls = mockGetCompose.mock.calls.length
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockGetCompose.mock.calls.length).toBeGreaterThan(calls))

    expect(view.text).not.toContain('hunter2')
  })

  it('clean editor, lock, before the refetch lands: the secret is not on screen', async () => {
    renderEditor()
    await waitFor(() => expect(view.text).toBe(REAL))

    act(() => useEnvUnlockStore.getState().lock())

    expect(view.text).not.toContain('hunter2')
  })

  it('control: clean editor, lock, the refetch lands masked: the masked file replaces it', async () => {
    const queryClient = renderEditor()
    await waitFor(() => expect(view.text).toBe(REAL))
    disk = { content: MASKED, maskedValues: 1 }

    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))

    await waitFor(() => expect(view.text).toBe(MASKED))
  })
})
