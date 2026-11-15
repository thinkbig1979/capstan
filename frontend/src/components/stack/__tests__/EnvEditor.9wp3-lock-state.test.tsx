/**
 * agent-os-9wp3: what a lock does to the unlocked values the editor holds.
 *
 * Decided (orch-dm-59 and Edwin, 2026-11-15): unsaved edits stay across a lock
 * in state (agent-os-vqf5, w6wx), masked on screen, and revealing one asks for
 * the unlock first. The newer file held behind the "changed on disk" notice is
 * different: it is a payload served unlocked, and Reload would load it while
 * locked, comment rows unflagged. The lock drops it. The notice comes back at
 * the next unlock, when the file then served still differs from the edits'
 * baseline, before a save is possible; a locked refetch does not raise it.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { queryKeys } from '@/lib/query-keys'

const mockGetEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
  },
  authApi: { verifyPassword: vi.fn() },
  apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { EnvEditor } from '../EnvEditor'

const file = (comment: string) => ({
  hasEnvFile: true,
  filename: '.env',
  entries: [
    { key: 'DB_PASSWORD', value: 'oldsecret', line: 1, sensitive: true },
    { key: '', value: comment, line: 2, sensitive: false, comment: true },
  ],
  raw: `DB_PASSWORD=oldsecret\n${comment}\n`,
})
const NOTICE = /This file changed on disk/

const val = (n: number) => screen.getAllByLabelText(`Environment variable value ${n}`)[0] as HTMLInputElement

async function dirtyUnlocked() {
  mockGetEnv.mockResolvedValue(file('# notes'))
  act(() => useEnvUnlockStore.getState().unlock('tok'))
  const r = renderWithProviders(<EnvEditor stackId="s1" />)
  await waitFor(() => expect(val(1).value).toBe('oldsecret'))
  fireEvent.change(val(1), { target: { value: 'typedsecret' } })
  return r
}

describe('EnvEditor: unlocked values across a lock (agent-os-9wp3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    act(() => useEnvUnlockStore.getState().lock())
    useAuthStore.setState({ authDisabled: false })
  })
  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
  })

  it('a kept edit stays in the form, masked, and revealing it asks for the unlock', async () => {
    await dirtyUnlocked()
    mockGetEnv.mockRejectedValue(new Error('offline'))
    act(() => useEnvUnlockStore.getState().lock())

    expect(val(1).value).toBe('typedsecret') // kept (vqf5, w6wx)
    expect(val(1).type).toBe('password')
    fireEvent.click(screen.getAllByLabelText('Toggle visibility for entry 1')[0])
    expect(val(1).type).toBe('password')
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('a newer file held behind the notice is dropped at the lock, so Reload cannot load it locked', async () => {
    const { queryClient } = await dirtyUnlocked()
    mockGetEnv.mockResolvedValue(file('#OLD_SECRET=commentedvalue'))
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument() // positive side: held

    mockGetEnv.mockRejectedValue(new Error('offline'))
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))

    expect(screen.queryByText(NOTICE)).toBeNull()
    expect(screen.queryAllByText('commentedvalue', { exact: false })).toHaveLength(0)
    expect(val(1).value).toBe('typedsecret')
  })

  it('the dropped notice comes back at the next unlock, not with a locked refetch', async () => {
    const { queryClient } = await dirtyUnlocked()
    const changed = file('#OLD_SECRET=commentedvalue')
    mockGetEnv.mockResolvedValue(changed)
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()

    mockGetEnv.mockResolvedValue({
      hasEnvFile: true,
      filename: '.env',
      locked: true,
      entries: [
        { key: 'DB_PASSWORD', value: '', line: 1, sensitive: true },
        { key: '', value: '#OLD_SECRET=', line: 2, sensitive: true, comment: true },
      ],
    })
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Unlock' })).toBeInTheDocument())
    expect(screen.queryByText(NOTICE)).toBeNull() // a locked refetch does not raise it

    mockGetEnv.mockResolvedValue(changed)
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.stack.env('s1') }))
    expect(await screen.findByText(NOTICE)).toBeInTheDocument()
    expect(val(1).value).toBe('typedsecret')
  })
})
