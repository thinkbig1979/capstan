import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

/**
 * agent-os-o5ud. GitHistory's two first-page guards keyed on the PAGINATION
 * CURSOR (`offset === 0`) where a DATA-PRESENCE test belongs, which is the
 * agent-os-wczm / agent-os-4gve class: `logData` survives a failed refetch and
 * is still read at GitHistory.tsx:32, :33, :37 and :49, so at offset 0 one
 * focus-refetch that 500s replaced a rendered commit list with "Failed to load
 * git history", and at offset > 0 the same failure showed stale commits with no
 * disclosure at all.
 *
 * Kept in its own file rather than added to GitHistory.test.tsx because every
 * case there mocks `@/hooks/useGit` and hands the component a frozen
 * `{isLoading, error, data}` triple. That fixture CANNOT express this bug: a
 * mocked hook has no query state, so there is no first fetch to resolve and no
 * refetch to reject. These cases need the real `useGitLog` over a real
 * QueryClient, so they mock the API layer underneath it instead.
 *
 * CAVEAT, stated rather than overclaimed: mocking `@/lib/api` wholesale bypasses
 * the axios response interceptor that actually builds `{ ...error.response.data,
 * status }` (api.ts:127-131). A green here proves the component HANDLES that
 * shape; it does not prove the wire PRODUCES it.
 */

const mockLog = vi.fn()
const mockDiff = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    log: (...args: unknown[]) => mockLog(...args),
    diff: (...args: unknown[]) => mockDiff(...args),
    status: vi.fn(),
    pull: vi.fn(),
  },
}))

import { GitHistory } from '../GitHistory'

const STACK_ID = 'stacks~myapp:default'

const pageOne = [
  {
    hash: 'abc123def456',
    short: 'abc123d',
    author: 'Jane Doe',
    email: 'jane@example.com',
    message: 'Fix container restart race condition',
    date: '2026-07-20T10:00:00Z',
  },
  {
    hash: 'def456abc789',
    short: 'def456a',
    author: 'John Smith',
    email: 'john@example.com',
    message: 'Add health check endpoint',
    date: '2026-07-19T09:00:00Z',
  },
]

const pageTwo = [
  {
    hash: '9f9f9f9f9f9f',
    short: '9f9f9f9',
    author: 'Ada Lovelace',
    email: 'ada@example.com',
    message: 'Pin the compose schema version',
    date: '2026-07-18T08:00:00Z',
  },
]

/** The flat shape api.ts's interceptor rejects with (api.ts:127-131). */
const serverFault = { status: 500, code: 'GIT_LOG_FAILED', message: 'git log failed' }

describe('GitHistory — a failed REFETCH is a refresh failure, not a load failure (agent-os-o5ud)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  /**
   * THE fail-first arm. Resolve first, THEN reject the refetch: a
   * first-fetch-rejects fixture is blind to this class, because it never puts a
   * commit list on screen for the guard to throw away.
   *
   * The discriminating assertion is the COMMIT LIST, not the notice. An arm that
   * only asserted the notice was missing would go red for a component that
   * correctly kept the commits and merely lacked wording, which proves nothing
   * about the defect this bead is about.
   */
  it('keeps the rendered commit list when a refetch fails at offset 0', async () => {
    mockLog.mockResolvedValue({ commits: pageOne, total: 2, hasMore: false })
    const { queryClient } = renderWithProviders(<GitHistory stackId={STACK_ID} />)

    expect(await screen.findByText('Fix container restart race condition')).toBeInTheDocument()

    mockLog.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    // The query REALLY reached the error state before anything below is read:
    // refetchQueries() can return before React re-renders, and an arm without
    // this passes against the defective code and proves nothing.
    await waitFor(() =>
      expect(queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error')).toBe(true),
    )

    expect(screen.getByText('Fix container restart race condition')).toBeInTheDocument()
    expect(screen.getByText('Add health check endpoint')).toBeInTheDocument()
    expect(screen.getByText('Showing 2 commits')).toBeInTheDocument()
    expect(screen.queryByText('Failed to load git history')).not.toBeInTheDocument()
    expect(
      screen.getByText(/Could not refresh the git history\. The values shown are the last ones the server sent\./),
    ).toBeInTheDocument()
  })

  /**
   * The OTHER half of the same defect, and the reason `offset > 0` is this
   * bead's business too: past the first page the old guard was simply never
   * reached, so a failed refetch showed stale commits with NO disclosure at all.
   * Re-keying both guards on data presence gives every page the same answer.
   */
  it('discloses a failed refetch past the first page, where the cursor guard never fired', async () => {
    mockLog.mockResolvedValue({ commits: pageOne, total: 3, hasMore: true })
    const { queryClient } = renderWithProviders(<GitHistory stackId={STACK_ID} />)

    expect(await screen.findByText('Fix container restart race condition')).toBeInTheDocument()

    mockLog.mockResolvedValue({ commits: pageTwo, total: 3, hasMore: false })
    fireEvent.click(screen.getByRole('button', { name: 'Load More' }))
    expect(await screen.findByText('Pin the compose schema version')).toBeInTheDocument()

    mockLog.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error')).toBe(true),
    )

    expect(screen.getByText('Pin the compose schema version')).toBeInTheDocument()
    expect(
      screen.getByText(/Could not refresh the git history\. The values shown are the last ones the server sent\./),
    ).toBeInTheDocument()
  })

  /**
   * The `isLoading` sibling at GitHistory.tsx:55 was the same cursor-keyed
   * mistake wearing a different word. Past the first page the query key changes
   * (useGit.ts:28-34 — a plain useQuery, no placeholderData), so `data` is
   * undefined while page 2 is in flight; the cursor guard did not fire and the
   * component fell through to the full render with an empty list.
   *
   * The discriminating string is "Showing 0 commits", MEASURED not assumed. The
   * empty-state sentence "No commits in this repository" is NOT reachable in
   * this window — GitHistory.tsx:186 gates it on `!isLoading`, and `isLoading`
   * is true for exactly this window — so asserting its absence would be
   * vacuous. The count line has no such gate: it renders
   * `Showing {filteredCommits.length} commit(s)` unconditionally, so the screen
   * stated a commit count of zero for a repository whose commits were still on
   * the wire. Both candidates were probed against the old guard before this arm
   * was written; only the count line fired.
   */
  it('says it is loading, not that there are zero commits, while a later page is in flight', async () => {
    mockLog.mockResolvedValue({ commits: pageOne, total: 3, hasMore: true })
    renderWithProviders(<GitHistory stackId={STACK_ID} />)

    expect(await screen.findByText('Fix container restart race condition')).toBeInTheDocument()

    let releasePageTwo: (value: unknown) => void = () => {}
    mockLog.mockReturnValue(new Promise((resolve) => { releasePageTwo = resolve }))
    fireEvent.click(screen.getByRole('button', { name: 'Load More' }))

    // Order is load-bearing: the FALSE STATEMENT is asserted first, so the red
    // names the defect ("Showing 0 commits" on screen) rather than a missing
    // spinner. fireEvent.click is act()-wrapped, so the re-render has already
    // flushed by here and a synchronous query is the right instrument.
    expect(screen.queryByText('Showing 0 commits')).not.toBeInTheDocument()
    expect(screen.getByText('Loading git history...')).toBeInTheDocument()

    releasePageTwo({ commits: pageTwo, total: 3, hasMore: false })
    expect(await screen.findByText('Pin the compose schema version')).toBeInTheDocument()
  })

  /**
   * PRESERVATION CONTROL. A FIRST load that fails has no commits to describe, so
   * the flat sentence and the backend's cause (agent-os-rtn8) are still the
   * right words and must survive the re-key. This one cannot fail first — it
   * passes against the old guard too — so it is a control, not evidence.
   */
  it('keeps the flat load-failure view when the FIRST fetch fails and there is nothing to show', async () => {
    mockLog.mockRejectedValue({ status: 404, code: 'GIT_NOT_REPO', message: 'Not a git repository' })
    renderWithProviders(<GitHistory stackId={STACK_ID} />)

    expect(await screen.findByText('Failed to load git history')).toBeInTheDocument()
    expect(screen.getByText('Not a git repository')).toBeInTheDocument()
    expect(screen.queryByText(/Could not refresh the git history/)).not.toBeInTheDocument()
  })

  /**
   * STALE-NOTICE control: the notice lives inside the `error` guard, so it
   * cannot outlive the failure it describes. Cannot fail first either — today's
   * component renders no notice at all — so it is pinned by mutation evidence.
   */
  it('drops the notice once a later refetch succeeds', async () => {
    mockLog.mockResolvedValue({ commits: pageOne, total: 2, hasMore: false })
    const { queryClient } = renderWithProviders(<GitHistory stackId={STACK_ID} />)
    expect(await screen.findByText('Fix container restart race condition')).toBeInTheDocument()

    mockLog.mockRejectedValue(serverFault)
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(screen.getByText(/Could not refresh the git history/)).toBeInTheDocument(),
    )

    mockLog.mockResolvedValue({ commits: pageOne, total: 2, hasMore: false })
    await queryClient.refetchQueries()

    await waitFor(() =>
      expect(screen.queryByText(/Could not refresh the git history/)).not.toBeInTheDocument(),
    )
    expect(screen.getByText('Fix container restart race condition')).toBeInTheDocument()
  })
})
