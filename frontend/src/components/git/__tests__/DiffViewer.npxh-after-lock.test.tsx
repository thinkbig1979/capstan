/**
 * agent-os-npxh (probe): a commit diff fetched while unlocked carries secrets in
 * the clear; the server masks them only for a locked session, and the diff wire
 * shape has no `locked` flag. After a lock the diff on screen is the unlocked one
 * until the lock's refetch lands, and for good if that refetch fails.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, act, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import { invalidateEnvUnlockQueries } from '@/lib/env-unlock-queries'

const mockDiff = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    diff: (...args: unknown[]) => mockDiff(...args),
  },
}))

import { DiffViewer } from '../DiffViewer'

const SECRET = 'hunter2-plaintext'
const diffOf = (password: string) =>
  [
    'diff --git a/compose.yml b/compose.yml',
    'index 1111111..2222222 100644',
    '--- a/compose.yml',
    '+++ b/compose.yml',
    '@@ -1,2 +1,2 @@',
    ' services:',
    `-      DB_PASSWORD: old`,
    `+      DB_PASSWORD: ${password}`,
  ].join('\n')
const result = (diff: string) => ({
  commit: { hash: 'abc1234', shortHash: 'abc1234', author: 'a', email: 'e', date: '', message: 'm' },
  diff,
  files: ['compose.yml'],
})

beforeEach(() => {
  vi.clearAllMocks()
  mockDiff.mockReset()
  act(() => useEnvUnlockStore.getState().lock())
  useAuthStore.setState({ authDisabled: false })
})
afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('DiffViewer after a lock (agent-os-npxh)', () => {
  it.each([
    ['the lock refetch fails', () => Promise.reject(new Error('offline'))],
    ['the lock refetch is still in flight', () => new Promise(() => {})],
  ])('%s: the unlocked diff is not on screen', async (_name, refetch) => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    mockDiff.mockResolvedValueOnce(result(diffOf(SECRET)))
    const { queryClient } = renderWithProviders(<DiffViewer stackId="s1" commitHash="abc1234" />)
    await waitFor(() => expect(screen.getAllByText(SECRET, { exact: false }).length).toBeGreaterThan(0))

    mockDiff.mockImplementationOnce(refetch)
    act(() => useEnvUnlockStore.getState().lock())
    act(() => {
      void invalidateEnvUnlockQueries(queryClient)
    })
    await waitFor(() => expect(mockDiff).toHaveBeenCalledTimes(2))

    expect(screen.queryAllByText(SECRET, { exact: false })).toHaveLength(0)
  })

  it('after the lock refetch fails, Retry fetches the diff again and shows the masked one', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    mockDiff.mockResolvedValueOnce(result(diffOf(SECRET)))
    const { queryClient } = renderWithProviders(<DiffViewer stackId="s1" commitHash="abc1234" />)
    await waitFor(() => expect(screen.getAllByText(SECRET, { exact: false }).length).toBeGreaterThan(0))

    mockDiff.mockImplementationOnce(() => Promise.reject(new Error('offline')))
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(screen.getByText('Failed to load diff')).toBeInTheDocument())

    mockDiff.mockResolvedValueOnce(result(diffOf('MASKEDVALUE')))
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.getAllByText('MASKEDVALUE', { exact: false }).length).toBeGreaterThan(0))
    expect(screen.queryAllByText(SECRET, { exact: false })).toHaveLength(0)
  })

  it('a lock while the unlocked request is in flight: the plaintext it brings back is not shown', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    let resolve: (v: unknown) => void = () => {}
    mockDiff.mockImplementationOnce(() => new Promise((r) => (resolve = r)))
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc1234" />)
    await waitFor(() => expect(mockDiff).toHaveBeenCalledTimes(1))

    act(() => useEnvUnlockStore.getState().lock())
    await act(async () => resolve(result(diffOf(SECRET))))
    await waitFor(() => expect(screen.queryByText(/Loading diff/)).toBeNull()) // the response has landed

    expect(screen.queryAllByText(SECRET, { exact: false })).toHaveLength(0)
    expect(screen.getByText('Failed to load diff')).toBeInTheDocument()
  })

  it('control: a diff fetched while locked stays on screen after the lock refetch fails', async () => {
    mockDiff.mockResolvedValueOnce(result(diffOf('***')))
    const { queryClient } = renderWithProviders(<DiffViewer stackId="s1" commitHash="abc1234" />)
    await waitFor(() => expect(screen.getAllByText('***', { exact: false }).length).toBeGreaterThan(0))

    mockDiff.mockImplementationOnce(() => Promise.reject(new Error('offline')))
    act(() => useEnvUnlockStore.getState().lock())
    await act(() => invalidateEnvUnlockQueries(queryClient))
    await waitFor(() => expect(mockDiff).toHaveBeenCalledTimes(2))

    expect(screen.getAllByText('***', { exact: false }).length).toBeGreaterThan(0)
  })
})
