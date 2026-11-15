/**
 * agent-os-xmbg: the server refuses a commit while env values are locked (a
 * locked log masks the message, so a commit of guesses would read back as an
 * oracle). The dialog disables Commit and Commit and push while locked, says
 * why with an unlock, and keeps Push; unlocked, Commit is offered.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, act, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { useAuthStore } from '@/stores/authStore'
import type { GitChangesResult } from '@/types/generated'

const mockChanges = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    changes: (...a: unknown[]) => mockChanges(...a),
    changesDiff: vi.fn().mockResolvedValue({ path: 'compose.yaml', hidden: false, diff: '' }),
    commit: vi.fn(),
    push: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { CommitPushDialog } from '../CommitPushDialog'

const CHANGES: GitChangesResult = {
  branch: 'main',
  detached: false,
  trackingBranch: 'origin/main',
  hasUpstream: true,
  ahead: 1,
  behind: 0,
  pushEnabled: true,
  identityConfigured: true,
  changes: [{ path: 'compose.yaml', status: '.M', isEnv: false }],
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ authDisabled: false })
  act(() => useEnvUnlockStore.getState().lock())
  mockChanges.mockResolvedValue(CHANGES)
})
afterEach(() => {
  act(() => useEnvUnlockStore.getState().lock())
})

describe('CommitPushDialog while locked (agent-os-xmbg)', () => {
  it('disables both commit buttons, says why, and keeps Push', async () => {
    renderWithProviders(<CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />)

    const commit = await screen.findByRole('button', { name: 'Commit' })
    // A message and a ticked file: only the lock can keep Commit off.
    fireEvent.change(screen.getByLabelText('Commit message'), { target: { value: 'm' } })
    expect(commit).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Commit and push' })).toBeDisabled()
    expect(screen.getByText(/Committing needs an unlock/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Push 1 commit' })).toBeEnabled()
  })

  it('offers Commit once unlocked, with no notice', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok'))
    renderWithProviders(<CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />)

    const commit = await screen.findByRole('button', { name: 'Commit' })
    fireEvent.change(screen.getByLabelText('Commit message'), { target: { value: 'm' } })
    expect(commit).toBeEnabled()
    expect(screen.queryByText(/Committing needs an unlock/)).toBeNull()
  })
})
