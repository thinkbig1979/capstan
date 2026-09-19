/**
 * DiffViewer was at 0/38 statements (agent-os-c1gu). It is reached only from
 * GitHistory's diff path, which GitHistory.test.tsx never opens.
 *
 * `@/lib/api` is mocked rather than `useGitDiff`, so the real query, the real
 * loading/error transitions and the REAL lib/diff-parser all run. That matters
 * here: the assertions below are written against the parser's actual output as of
 * agent-os-t9up (paths stripped of their `a/`/`b/` prefixes, `/dev/null`
 * recognised), which is what stops this bead and that one contradicting each
 * other.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

const mockDiff = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    diff: (...args: unknown[]) => mockDiff(...args),
  },
}))

import { DiffViewer } from '../DiffViewer'

const DIFF = [
  'diff --git a/compose.yml b/compose.yml',
  'index 1111111..2222222 100644',
  '--- a/compose.yml',
  '+++ b/compose.yml',
  '@@ -1,3 +1,3 @@',
  ' services:',
  '   web:',
  '-    image: nginx:1.0',
  '+    image: nginx:2.0',
].join('\n')

const TWO_FILE_DIFF = [
  DIFF,
  'diff --git a/README.md b/README.md',
  '--- a/README.md',
  '+++ b/README.md',
  '@@ -1 +1,2 @@',
  ' # Title',
  '+a new line',
].join('\n')

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  // Radix Select needs these three jsdom stubs or its popup never opens.
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

afterEach(() => {
  localStorage.clear()
})

describe('DiffViewer — load states', () => {
  it('shows a loading message while the diff is in flight', () => {
    mockDiff.mockReturnValue(new Promise(() => {}))
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(screen.getByText('Loading diff...')).toBeInTheDocument()
  })

  it('reports a failure when the request rejects', async () => {
    mockDiff.mockRejectedValue(new Error('boom'))
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('Failed to load diff')).toBeInTheDocument()
  })

  /**
   * agent-os-rtn8. GetDiff (handlers/git.go) answers with more than one
   * refusal and this branch rendered one sentence for all of them: a 400
   * "Invalid commit hash format" minted in the handler itself (git.go:299),
   * plus the 404s resolvePathFromStack and services/git.go return through
   * handleError at :292 and :309 -- "Stack not found", "Not a git repository".
   * A malformed hash and a missing repository need different operator actions.
   *
   * The fixture is the FLAT shape api.ts's interceptor rejects with
   * (api.ts:127-131), which is what actually reaches the component.
   */
  it.each([
    [400, 'VALIDATION_ERROR', 'Invalid commit hash format'],
    [404, 'GIT_NOT_REPO', 'Not a git repository'],
    [404, 'NOT_FOUND', 'Stack not found'],
  ])('names the cause the backend sent for a %s %s failure', async (status, code, message) => {
    mockDiff.mockRejectedValue({ status, code, message })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('Failed to load diff')).toBeInTheDocument()
    expect(screen.getByText(message)).toBeInTheDocument()
  })

  /**
   * The multi-condition half, and the reason the cause is gated on `error`
   * alone rather than on the whole branch condition. `error || !diffData`
   * routes TWO states here and only the first carries an error: a request that
   * RESOLVES with nothing has no cause, and manufacturing one would be the
   * worse defect. Same shape #408 pinned on BackupSettingsContent.
   *
   * This is also the two-sided arm (agent-os-zlw0 criterion 2): the fixed
   * sentence stays as the headline, so this case renders exactly what it
   * rendered before the cause was ever added.
   */
  it('adds no cause when the request resolved with no payload', async () => {
    mockDiff.mockResolvedValue(undefined)
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('Failed to load diff')).toBeInTheDocument()
    expect(screen.queryByText('An unexpected error occurred')).not.toBeInTheDocument()
    expect(screen.queryByText('An error occurred')).not.toBeInTheDocument()
  })

  it('says there is no diff when the response carries no parseable files', async () => {
    mockDiff.mockResolvedValue({ diff: '' })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('No diff available')).toBeInTheDocument()
  })

  it('requests the diff for the stack and commit it was given', async () => {
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await waitFor(() => expect(mockDiff).toHaveBeenCalledWith('s1', 'abc123'))
  })
})

describe('DiffViewer — rendering a diff', () => {
  it('names the file without the git a/ b/ prefix', async () => {
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    // Asserted against the parser as of agent-os-t9up: before that fix this read
    // "b/compose.yml".
    expect(await screen.findByText('compose.yml')).toBeInTheDocument()
    expect(screen.queryByText('b/compose.yml')).not.toBeInTheDocument()
  })

  it('shows the added and removed line counts', async () => {
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('+1')).toBeInTheDocument()
    expect(screen.getByText('-1')).toBeInTheDocument()
  })

  it('omits a zero count rather than showing "+0"', async () => {
    // README.md in this diff is add-only, so it must show +1 and no -0.
    mockDiff.mockResolvedValue({
      diff: [
        'diff --git a/README.md b/README.md',
        '--- a/README.md',
        '+++ b/README.md',
        '@@ -1 +1,2 @@',
        ' # Title',
        '+a new line',
      ].join('\n'),
    })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('+1')).toBeInTheDocument()
    expect(screen.queryByText('-0')).not.toBeInTheDocument()
  })

  it('renders the hunk header and both sides of the change', async () => {
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('@@ -1,3 +1,3 @@')).toBeInTheDocument()
    expect(screen.getByText(/-\s+image: nginx:1\.0/)).toBeInTheDocument()
    expect(screen.getByText(/\+\s+image: nginx:2\.0/)).toBeInTheDocument()
  })

  it('renders every file in a multi-file diff', async () => {
    mockDiff.mockResolvedValue({ diff: TWO_FILE_DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('compose.yml')).toBeInTheDocument()
    expect(screen.getByText('README.md')).toBeInTheDocument()
  })
})

describe('DiffViewer — collapsing files', () => {
  it('hides a file\'s hunks when its header is clicked, and restores them', async () => {
    const user = userEvent.setup()
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('@@ -1,3 +1,3 @@')

    await user.click(screen.getByRole('button', { name: /compose\.yml/ }))
    await waitFor(() => {
      expect(screen.queryByText('@@ -1,3 +1,3 @@')).not.toBeInTheDocument()
    })
    // The file is still listed — collapsed, not removed.
    expect(screen.getByText('compose.yml')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /compose\.yml/ }))
    expect(await screen.findByText('@@ -1,3 +1,3 @@')).toBeInTheDocument()
  })

  it('collapses only the file that was clicked', async () => {
    const user = userEvent.setup()
    mockDiff.mockResolvedValue({ diff: TWO_FILE_DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('@@ -1,3 +1,3 @@')
    expect(screen.getByText('@@ -1 +1,2 @@')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /compose\.yml/ }))

    await waitFor(() => {
      expect(screen.queryByText('@@ -1,3 +1,3 @@')).not.toBeInTheDocument()
    })
    // Collapse state is keyed by path, so README.md must be untouched.
    expect(screen.getByText('@@ -1 +1,2 @@')).toBeInTheDocument()
  })
})

describe('DiffViewer — unified and split views', () => {
  it('defaults to unified, which puts each line in a single cell', async () => {
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('compose.yml')
    expect(screen.getByRole('combobox')).toHaveTextContent('Unified')

    const removed = screen.getByText(/-\s+image: nginx:1\.0/)
    const row = removed.closest('tr')!
    expect(within(row).getAllByRole('cell')).toHaveLength(1)
  })

  it('switching to split puts old and new side by side', async () => {
    const user = userEvent.setup()
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('compose.yml')
    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: 'Split' }))

    await waitFor(() => {
      const removed = screen.getByText(/^-\s+image: nginx:1\.0$/)
      expect(within(removed.closest('tr')!).getAllByRole('cell')).toHaveLength(2)
    })
  })

  it('persists the chosen view to localStorage', async () => {
    const user = userEvent.setup()
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('compose.yml')
    // Written on mount too, so the default is recorded rather than left absent.
    expect(localStorage.getItem('diff-view-preference')).toBe('unified')

    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: 'Split' }))

    await waitFor(() => {
      expect(localStorage.getItem('diff-view-preference')).toBe('split')
    })
  })

  it('restores a saved split preference on mount', async () => {
    localStorage.setItem('diff-view-preference', 'split')
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('compose.yml')
    expect(screen.getByRole('combobox')).toHaveTextContent('Split')
  })

  it('falls back to unified when the saved preference is not a known view', async () => {
    localStorage.setItem('diff-view-preference', 'something-else')
    mockDiff.mockResolvedValue({ diff: DIFF })
    renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    await screen.findByText('compose.yml')
    expect(screen.getByRole('combobox')).toHaveTextContent('Unified')
  })
})

// ─── agent-os-lurn: a failed REFETCH must not discard the diff ───────────────

/**
 * agent-os-lurn, the agent-os-wczm class written with the query's `error`
 * OBJECT rather than the identifier `isError`, which is why wczm's eslint
 * ratchet could not see it.
 *
 * `error` is set for a failed REFETCH exactly as for a failed first load and
 * TanStack retains `data` in both cases, so `if (error || !diffData)` replaced
 * a rendered diff with "Failed to load diff" after one focus refetch that 500s
 * (staleTime 30s, refetchOnWindowFocus on, a 500 is not auto-retryable).
 *
 * This arm RESOLVES first and rejects a REFETCH, and asserts the query really
 * reached the error state before the discriminating assertions. Without that
 * assertion the arm is satisfied by a render that never saw the rejection —
 * which passes against the defective guard and proves nothing.
 *
 * The two preservation controls for this change are the PRE-EXISTING arms
 * "reports a failure when the request rejects" (first load, no data) and
 * "adds no cause when the request resolved with no payload" (the agent-os-rtn8
 * case). Neither can fail first; both are pinned by mutation evidence.
 */
describe('DiffViewer — a failed REFETCH must not discard the diff (agent-os-lurn)', () => {
  it('keeps the rendered diff when a REFETCH fails', async () => {
    mockDiff.mockResolvedValue({ diff: DIFF })
    const { queryClient } = renderWithProviders(<DiffViewer stackId="s1" commitHash="abc123" />)

    expect(await screen.findByText('compose.yml')).toBeInTheDocument()

    mockDiff.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries()
    await waitFor(() =>
      expect(queryClient.getQueryCache().getAll().some((q) => q.state.status === 'error')).toBe(true),
    )

    // The diff the server already sent is still rendered...
    expect(screen.getByText('compose.yml')).toBeInTheDocument()
    // ...and the failure headline has NOT replaced it.
    expect(screen.queryByText('Failed to load diff')).not.toBeInTheDocument()
    // ...and the refresh failure is reported rather than swallowed.
    expect(
      screen.getByText(/Could not refresh the diff\. The values shown are the last ones the server sent\./),
    ).toBeInTheDocument()
    expect(screen.queryByText(/check them before saving/)).not.toBeInTheDocument()
  })
})
