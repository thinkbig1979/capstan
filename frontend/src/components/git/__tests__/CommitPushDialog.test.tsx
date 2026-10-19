/**
 * The commit and push dialog (agent-os-k81p.3): what it offers, what it
 * refuses to offer, and how it reports what the server did. Every git call
 * goes through the mocked gitApi, so the call list is what the dialog sent.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient } from '@tanstack/react-query'
import { renderWithProviders } from '../../../test/utils'
import type { GitChange, GitChangesResult } from '@/types/generated'

const mockChanges = vi.fn()
const mockChangesDiff = vi.fn()
const mockCommit = vi.fn()
const mockPush = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    changes: (...a: unknown[]) => mockChanges(...a),
    changesDiff: (...a: unknown[]) => mockChangesDiff(...a),
    commit: (...a: unknown[]) => mockCommit(...a),
    push: (...a: unknown[]) => mockPush(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { CommitPushDialog } from '../CommitPushDialog'

// The D136 sentence, as the user reads it.
const ENV_DIFF_HIDDEN = 'Diff hidden: env files may contain secrets.'

const COMPOSE: GitChange = { path: 'compose.yaml', status: '.M', isEnv: false }
const ENV: GitChange = { path: '.env', status: '.M', isEnv: true }
const OTHER: GitChange = { path: 'config/app.yml', status: '.M', isEnv: false }

function changesResult(over: Partial<GitChangesResult> = {}): GitChangesResult {
  return {
    branch: 'main',
    detached: false,
    trackingBranch: 'origin/main',
    hasUpstream: true,
    ahead: 0,
    behind: 0,
    pushEnabled: true,
    identityConfigured: true,
    changes: [COMPOSE, ENV, OTHER],
    ...over,
  }
}

const DIFF = 'diff --git a/compose.yaml b/compose.yaml\n--- a/compose.yaml\n+++ b/compose.yaml\n@@ -1 +1 @@\n-services: {}\n+services: {a: {}}\n'

function renderDialog() {
  return renderWithProviders(
    <CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />,
  )
}

const checkbox = (path: string) => screen.getByRole('checkbox', { name: new RegExp(`^${path.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')}`) })
const commitButton = () => screen.getByRole('button', { name: 'Commit' })
const commitPushButton = () => screen.getByRole('button', { name: 'Commit and push' })

async function typeMessage(text: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Commit message'), text)
}

beforeEach(() => {
  vi.clearAllMocks()
  mockChanges.mockResolvedValue(changesResult())
  mockChangesDiff.mockResolvedValue({ path: 'compose.yaml', hidden: false, diff: DIFF })
})

describe('CommitPushDialog: the file list', () => {
  it('pre-ticks the compose file and leaves env files and the rest unticked', async () => {
    renderDialog()
    expect(await screen.findByRole('checkbox', { name: /^compose\.yaml/ })).toBeChecked()
    expect(checkbox('.env')).not.toBeChecked()
    expect(checkbox('config/app.yml')).not.toBeChecked()
    expect(screen.getByText('env file, may contain secrets')).toBeInTheDocument()
  })

  it('an env file needs its own confirm before either commit button is enabled', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByRole('checkbox', { name: /^compose\.yaml/ })
    await typeMessage('update')
    expect(commitButton()).toBeEnabled()
    expect(screen.queryByRole('checkbox', { name: 'Commit the env file anyway' })).not.toBeInTheDocument()

    await user.click(checkbox('.env'))
    expect(screen.getByText(/An env file is ticked/)).toBeInTheDocument()
    expect(commitButton()).toBeDisabled()
    expect(commitPushButton()).toBeDisabled()

    await user.click(screen.getByRole('checkbox', { name: 'Commit the env file anyway' }))
    expect(commitButton()).toBeEnabled()
    await user.click(commitButton())
    expect(mockCommit).toHaveBeenCalledTimes(1)
    expect(mockCommit).toHaveBeenCalledWith({
      stackId: 's1', message: 'update', files: ['compose.yaml', '.env'], confirmEnv: true, push: false,
    })
  })

  it('unticking every env file clears the confirm, so re-ticking asks again (review R3 N1)', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByRole('checkbox', { name: /^compose\.yaml/ })
    await typeMessage('update')
    await user.click(checkbox('.env'))
    await user.click(screen.getByRole('checkbox', { name: 'Commit the env file anyway' }))
    expect(commitButton()).toBeEnabled()
    await user.click(checkbox('.env'))
    await user.click(checkbox('.env'))
    expect(screen.getByRole('checkbox', { name: 'Commit the env file anyway' })).not.toBeChecked()
    expect(commitButton()).toBeDisabled()
  })

  it('shows the selected file\'s diff, and never requests an env file\'s (D136)', async () => {
    const user = userEvent.setup()
    renderDialog()
    // The compose file is selected first, and its diff renders.
    expect(await screen.findByText(/services: \{a: \{\}\}/)).toBeInTheDocument()
    expect(mockChangesDiff).toHaveBeenCalledWith('s1', 'compose.yaml')

    await user.click(screen.getByRole('button', { name: 'Show diff of .env' }))
    expect(screen.getByText(ENV_DIFF_HIDDEN)).toBeInTheDocument()
    expect(screen.queryByText(/services: \{a: \{\}\}/)).not.toBeInTheDocument()
    expect(mockChangesDiff.mock.calls.map((c) => c[1])).not.toContain('.env')
  })

  it('re-reads a diff cached before the latest save (review R3 S3)', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 30_000 } } })
    queryClient.setQueryData(['git', 's1', 'changes', 'diff', 'compose.yaml'], {
      path: 'compose.yaml', hidden: false,
      diff: 'diff --git a/compose.yaml b/compose.yaml\n--- a/compose.yaml\n+++ b/compose.yaml\n@@ -1 +1 @@\n-services: {}\n+services: {old: {}}\n',
    })
    renderWithProviders(<CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />, { queryClient })
    expect(await screen.findByText(/services: \{a: \{\}\}/)).toBeInTheDocument()
    expect(screen.queryByText(/services: \{old: \{\}\}/)).not.toBeInTheDocument()
    expect(mockChangesDiff).toHaveBeenCalledWith('s1', 'compose.yaml')
  })

  it('shows the D136 sentence when the server hides a file the list did not mark', async () => {
    const user = userEvent.setup()
    mockChangesDiff.mockResolvedValue({ path: 'config/app.yml', hidden: true, diff: '' })
    renderDialog()
    await screen.findByRole('checkbox', { name: /^compose\.yaml/ })
    await user.click(screen.getByRole('button', { name: 'Show diff of config/app.yml' }))
    expect(await screen.findByText(ENV_DIFF_HIDDEN)).toBeInTheDocument()
  })
})

describe('CommitPushDialog: the message', () => {
  it('starts blank, and a blank or whitespace message blocks both commit buttons', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByRole('checkbox', { name: /^compose\.yaml/ })
    expect(screen.getByLabelText('Commit message')).toHaveValue('')
    expect(commitButton()).toBeDisabled()
    expect(commitPushButton()).toBeDisabled()

    await typeMessage('   ')
    await user.tab()
    expect(commitButton()).toBeDisabled()
    expect(commitPushButton()).toBeDisabled()
    expect(screen.getByLabelText('Commit message')).toHaveAccessibleDescription('A commit message is required.')

    await typeMessage('x')
    expect(commitButton()).toBeEnabled()
    expect(commitPushButton()).toBeEnabled()
  })
})

describe('CommitPushDialog: outcomes', () => {
  async function commitAndPush() {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByRole('checkbox', { name: /^compose\.yaml/ })
    await typeMessage('update')
    await user.click(commitPushButton())
    expect(mockCommit).toHaveBeenCalledWith({
      stackId: 's1', message: 'update', files: ['compose.yaml'], confirmEnv: false, push: true,
    })
  }

  it('success says what the server did', async () => {
    mockCommit.mockResolvedValue({
      outcome: 'success', reason: 'committed and pushed main to origin',
      details: { commit: 'abc1234def', files: 1 },
    })
    await commitAndPush()
    expect(await screen.findByRole('status')).toHaveTextContent('Commit abc1234: committed and pushed main to origin')
  })

  it('partial (207) keeps the commit and gives the push reason, never as a success', async () => {
    mockCommit.mockResolvedValue({
      outcome: 'partial', reason: 'committed, but the push failed: The remote has commits this branch does not have; pull first',
      details: { commit: 'abc1234def', files: 1, pushCode: 'GIT_REMOTE_AHEAD', pushReason: 'The remote has commits this branch does not have; pull first' },
    })
    await commitAndPush()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Committed abc1234, but the push failed. The commit is kept.')
    expect(alert).toHaveTextContent('The remote has newer commits. Pull first.')
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('a classified refusal (details.code) reads as its sentence', async () => {
    mockCommit.mockRejectedValue({
      outcome: 'failed', reason: 'Remote origin pushes over SSH, but no SSH key applies to this directory',
      details: { code: 'GIT_SSH_UNSUPPORTED' }, status: 409,
    })
    await commitAndPush()
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'No SSH key applies to this folder. Set its git auth to SSH with a key path, set the global SSH key in Settings > Git, or use an https remote.',
    )
  })

  it('an unusable SSH key shows the server message, which names the path and the fix (agent-os-z0gl)', async () => {
    const reason = 'This directory\'s SSH key "/app/keys/id" is readable by other users (mode 0644), which ssh refuses; run chmod 600 on it'
    mockCommit.mockRejectedValue({ outcome: 'failed', reason, details: { code: 'GIT_SSH_KEY_UNUSABLE' }, status: 409 })
    await commitAndPush()
    expect(await screen.findByRole('alert')).toHaveTextContent(reason)
  })

  it('a handleError refusal (top-level code) reads as its sentence', async () => {
    mockCommit.mockRejectedValue({ code: 'OPERATION_IN_PROGRESS', message: 'stack s1 is busy', status: 409 })
    await commitAndPush()
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Another operation is running on a stack in this repository. Try again in a moment.',
    )
  })

  it('a code with no sentence shows the server\'s own message', async () => {
    mockCommit.mockRejectedValue({
      outcome: 'failed', reason: 'The remote refused the push: pre-receive hook declined',
      details: { code: 'GIT_PUSH_REJECTED' }, status: 409,
    })
    await commitAndPush()
    expect(await screen.findByRole('alert')).toHaveTextContent('The remote refused the push: pre-receive hook declined')
  })
})

describe('CommitPushDialog: states', () => {
  it('no changes: says so, offers no commit, and offers Push when the branch is ahead of its upstream', async () => {
    const user = userEvent.setup()
    mockChanges.mockResolvedValue(changesResult({ changes: [], ahead: 2 }))
    mockPush.mockResolvedValue({ outcome: 'success', reason: 'pushed main to origin', details: {} })
    renderDialog()
    expect(await screen.findByText(/No uncommitted changes/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Push 2 commits' }))
    expect(mockPush).toHaveBeenCalledWith('s1')
    expect(mockCommit).not.toHaveBeenCalled()
  })

  it('ahead without an upstream offers no Push', async () => {
    mockChanges.mockResolvedValue(changesResult({ changes: [], ahead: 2, hasUpstream: false }))
    renderDialog()
    expect(await screen.findByText(/No uncommitted changes/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Push/ })).not.toBeInTheDocument()
  })

  it('push switch off: blocks both commits and names where to turn it on', async () => {
    mockChanges.mockResolvedValue(changesResult({ pushEnabled: false }))
    renderDialog()
    expect(await screen.findByText(/Commit and push are off for this folder/)).toHaveTextContent(
      /Allow commit and push.*git chip.*Git Credentials/,
    )
    await typeMessage('m')
    expect(commitButton()).toBeDisabled()
    expect(commitPushButton()).toBeDisabled()
  })

  it('push switch off: Push is disabled too, even with commits to push (review R3 FA)', async () => {
    mockChanges.mockResolvedValue(changesResult({ pushEnabled: false, changes: [], ahead: 1 }))
    renderDialog()
    expect(await screen.findByRole('button', { name: 'Push 1 commit' })).toBeDisabled()
  })

  it('identity missing: blocks commit and links to Settings > Git', async () => {
    mockChanges.mockResolvedValue(changesResult({ identityConfigured: false }))
    renderDialog()
    const link = await screen.findByRole('link', { name: /Settings > Git/ })
    expect(link).toHaveAttribute('href', '/settings/git')
    await typeMessage('m')
    expect(commitButton()).toBeDisabled()
    expect(commitPushButton()).toBeDisabled()
  })

  it('detached HEAD: both commit buttons and Push are disabled (the server refuses even push:false)', async () => {
    mockChanges.mockResolvedValue(changesResult({ detached: true, branch: '', ahead: 1 }))
    renderDialog()
    expect(await screen.findByText(/HEAD is detached/)).toBeInTheDocument()
    await typeMessage('m')
    expect(commitButton()).toBeDisabled()
    expect(commitPushButton()).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Push 1 commit' })).toBeDisabled()
  })

  it('no upstream: Commit is enabled and Commit and push is not', async () => {
    mockChanges.mockResolvedValue(changesResult({ hasUpstream: false }))
    renderDialog()
    expect(await screen.findByText(/has no upstream/)).toBeInTheDocument()
    await typeMessage('m')
    expect(commitButton()).toBeEnabled()
    expect(commitPushButton()).toBeDisabled()
  })

  it('a failed re-read over a list cached before opening shows the failure, not the stale list (review R3 NEW-1)', async () => {
    const user = userEvent.setup()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 30_000 } } })
    queryClient.setQueryData(['git', 's1', 'changes'], changesResult({ changes: [], ahead: 1 }))
    mockChanges.mockRejectedValueOnce({ code: 'INTERNAL_ERROR', message: 'Failed to read status', status: 500 })
    renderWithProviders(<CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />, { queryClient })
    expect(await screen.findByText(/Could not load the changed files/)).toBeInTheDocument()
    expect(screen.queryByText(/No uncommitted changes/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Commit/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Push/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('checkbox', { name: /^compose\.yaml/ })).toBeChecked()
  })

  it('a failed refresh after a fresh read keeps the list and says the refresh failed', async () => {
    const { queryClient } = renderDialog()
    expect(await screen.findByRole('checkbox', { name: /^compose\.yaml/ })).toBeChecked()
    mockChanges.mockRejectedValueOnce({ code: 'INTERNAL_ERROR', message: 'Failed to read status', status: 500 })
    await queryClient.refetchQueries({ queryKey: ['git', 's1', 'changes'] })
    expect(await screen.findByText(/Could not refresh the changed files/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /^compose\.yaml/ })).toBeChecked()
    expect(screen.queryByText(/Could not load the changed files/)).not.toBeInTheDocument()
  })

  it('a failed re-read over a diff cached before opening shows the failure, not the stale diff (review R3 NEW-1)', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 30_000 } } })
    queryClient.setQueryData(['git', 's1', 'changes', 'diff', 'compose.yaml'], {
      path: 'compose.yaml', hidden: false,
      diff: 'diff --git a/compose.yaml b/compose.yaml\n--- a/compose.yaml\n+++ b/compose.yaml\n@@ -1 +1 @@\n-services: {}\n+services: {old: {}}\n',
    })
    mockChangesDiff.mockRejectedValueOnce({ code: 'INTERNAL_ERROR', message: 'Failed to read the diff', status: 500 })
    renderWithProviders(<CommitPushDialog stackId="s1" composeFile="compose.yaml" open onOpenChange={vi.fn()} />, { queryClient })
    expect(await screen.findByText(/Could not load the diff/)).toBeInTheDocument()
    expect(screen.queryByText(/services: \{old: \{\}\}/)).not.toBeInTheDocument()
  })

  it('a failed load shows the error and Retry, and nothing to commit', async () => {
    const user = userEvent.setup()
    mockChanges.mockRejectedValueOnce({ code: 'GIT_BARE_REPO', message: 'A bare repository has no work tree to commit from', status: 409 })
    renderDialog()
    expect(await screen.findByText(/Could not load the changed files/)).toBeInTheDocument()
    expect(screen.getByText('A bare repository has no work tree to commit from')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(mockChanges).toHaveBeenCalledTimes(2))
    expect(await screen.findByRole('checkbox', { name: /^compose\.yaml/ })).toBeChecked()
  })
})
