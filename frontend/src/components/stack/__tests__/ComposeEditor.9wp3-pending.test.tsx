/**
 * agent-os-9wp3 (review F11), the compose editor: a newer file held behind the
 * "changed on disk" notice was served unlocked. The lock drops it, as the env
 * editors do; before, Reload loaded it, and only xz6h's hide of a clean stale
 * view kept its text off screen. Mocks as ComposeEditor.external-change.test.tsx.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

const view = { text: '' }
let capturedOnChange: ((content: string) => void) | undefined

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn((_ref: unknown, opts: { doc: string; docKey?: unknown; onChange?: (c: string) => void }) => {
    const viewRef = useRef<unknown>(null)
    viewRef.current = {
      state: { doc: { toString: () => view.text }, selection: { main: { from: 0, to: 0 } } },
      dispatch: vi.fn(),
      destroy: vi.fn(),
    }
    capturedOnChange = opts.onChange
    useEffect(() => {
      if (opts.doc !== view.text) view.text = opts.doc
    }, [opts.doc, opts.docKey])
    return { viewRef, isDark: false }
  }),
}))

vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

const mockGetCompose = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: (...args: unknown[]) => mockGetCompose(...args),
    updateCompose: vi.fn(),
    lintCompose: vi.fn().mockResolvedValue({ lintResults: [] }),
    get: vi.fn(),
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
  gitApi: { pull: vi.fn() },
  authApi: { unlockEnv: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ComposeEditor } from '../ComposeEditor'

const NOTICE = 'This file changed on disk. Reload to see the new version; saving now overwrites it.'
const ORIGINAL = 'services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: hunter2\n'
const CHANGED = 'services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: hunter3\n'
const EDITED = 'services:\n  db:\n    image: postgres:17\n'

beforeEach(() => {
  vi.clearAllMocks()
  view.text = ''
  capturedOnChange = undefined
  useAuthStore.setState({ authDisabled: false })
  act(() => useEnvUnlockStore.getState().unlock('tok'))
})
afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('ComposeEditor: a held file across a lock (agent-os-9wp3)', () => {
  it('the lock drops the held file; the edits stay', async () => {
    mockGetCompose.mockResolvedValue({ content: ORIGINAL, maskedValues: 0, locked: false })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    renderWithProviders(<ComposeEditor stackId="s1" />, { queryClient })
    await waitFor(() => expect(view.text).toBe(ORIGINAL))
    await waitFor(() => expect(capturedOnChange).toBeDefined())
    act(() => {
      view.text = EDITED
      capturedOnChange?.(EDITED)
    })

    mockGetCompose.mockResolvedValue({ content: CHANGED, maskedValues: 0, locked: false })
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.compose('s1') }))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument() // positive side: held

    mockGetCompose.mockRejectedValue(new Error('offline'))
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.compose('s1') }))

    expect(screen.queryByText(NOTICE)).toBeNull()
    expect(view.text).toBe(EDITED)
    expect(view.text).not.toContain('hunter3')
  })
})
