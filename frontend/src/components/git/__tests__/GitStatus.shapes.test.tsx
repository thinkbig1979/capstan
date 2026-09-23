/**
 * agent-os-ygqe. The frontend side of the directory shapes the backend
 * enumerates in services/git_notrepo_test.go, so the two sides fail together.
 *
 * Why this exists: agent-os-a786 once gated the git request on
 * `stack.isGitRepo`, and every gate stayed green. The backend rows assert what
 * an endpoint RETURNS; the defect was the UI deciding not to ask, which a
 * response assertion cannot observe. So every row here asserts, first, that
 * the request is ISSUED, and only then what renders.
 *
 * `@/lib/api` is mocked rather than `useGitStatus`, so the real hook and its
 * queryFn run and the `gitApi.status` spy sees the request itself. A mocked
 * hook cannot see a gate that lives in the hook's `enabled` option.
 *
 * WHAT EACH ROW'S ANSWER IS. The backend table tests GetLog/GetDiff/
 * GetLogForFile, not GetStatus, and GitStatus consumes GET /api/v1/git. The
 * answers below are that endpoint's, measured by calling GitService.GetStatus
 * per shape through handlers/git.go GetStatus's mapping (at 2fe7ca0):
 *   not a repo        -> 200 {isRepo: false}  (agent-os-x40a)
 *   directory is gone -> 404 STACK_DIR_MISSING
 *   subdirectory      -> 200 with the parent repo's real branch
 *   bare repo         -> 500 INTERNAL_ERROR ("this operation must be run in a
 *                        work tree"): a backend defect filed separately
 *
 * WHY EVERY ROW RUNS WITH BOTH VALUES OF `isGitRepo`. agent-os-ygqe was filed
 * saying the scanner reports isGitRepo=false for the nested and bare shapes.
 * That is ROTTED: the scanner now walks up and recognises bare repositories
 * (agent-os-yy00, agent-os-ozt0), and resolveGitState returns true for both.
 * With only today's values, the a786 gate would pass every row. It is still
 * the wrong predicate, because the field is a CACHE refreshed only by a scan
 * (agent-os-omvy): a directory that becomes a repository after the last scan
 * keeps isGitRepo=false. So each row runs once with the scanner's current value
 * and once with the other value. Under the a786 gate all four false-valued arms
 * go red on the request assertion; the stale-cache arms of nested and bare are
 * the named proof. The true-valued arms, which include today's scan of nested
 * and bare, are green under it by construction: they pin the render, not the ask.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { queryKeys } from '@/lib/query-keys'

const mockStatus = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    status: (...args: unknown[]) => mockStatus(...args),
    pull: vi.fn(),
  },
  directoriesApi: {
    list: vi.fn().mockResolvedValue([]),
    updateCredentials: vi.fn(),
    scan: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

import { GitStatus } from '../GitStatus'

function stackFor(isGitRepo: boolean) {
  return {
    envFile: '',
    gitBranch: '',
    gitCommit: '',
    containers: [],
    id: 'myapp:default',
    directory: '/opt/stacks/myapp',
    composeFile: 'docker-compose.yaml',
    projectName: 'myapp',
    status: 'running' as const,
    isGitRepo,
    gitDirty: false,
    gitAhead: 0,
    gitBehind: 0,
  }
}

// The rejection shape lib/api.ts's response interceptor produces: the body
// spread flat with the HTTP status beside it.
function apiError(status: number, code: string, error: string) {
  return { status, code, error, message: error }
}

type Shape = {
  name: string
  /** What resolveGitState returns for this shape today. */
  scannerIsGitRepo: boolean
  answer: () => Promise<unknown>
  expectRender: (container: HTMLElement) => void
}

const SHAPES: Shape[] = [
  {
    name: 'not a repo',
    scannerIsGitRepo: false,
    answer: () => Promise.resolve({ isRepo: false }),
    expectRender: () => expect(screen.getByText('not a git repository')).toBeInTheDocument(),
  },
  {
    name: 'directory is gone',
    scannerIsGitRepo: false,
    answer: () =>
      Promise.reject(apiError(404, 'STACK_DIR_MISSING', 'Stack directory does not exist on disk')),
    // agent-os-528x: a failed request renders nothing today.
    expectRender: (container) => expect(container).toBeEmptyDOMElement(),
  },
  {
    name: 'subdirectory of a repo',
    scannerIsGitRepo: true,
    answer: () =>
      Promise.resolve({
        isRepo: true,
        hasCommits: true,
        branch: 'main',
        commit: 'abc123',
        commitShort: 'abc1234',
        commitMessage: 'seed',
        commitAuthor: 't',
        commitDate: '2026-09-23',
        dirty: false,
        dirtyCount: 0,
        ahead: 0,
        behind: 0,
      }),
    expectRender: () => expect(screen.getByRole('button', { name: /Git status: main/ })).toBeInTheDocument(),
  },
  {
    name: 'bare repo with commits',
    scannerIsGitRepo: true,
    answer: () =>
      Promise.reject(apiError(500, 'INTERNAL_ERROR', 'An internal error occurred')),
    // agent-os-528x: a failed request renders nothing today.
    expectRender: (container) => expect(container).toBeEmptyDOMElement(),
  },
]

const ARMS = SHAPES.flatMap((shape) =>
  [shape.scannerIsGitRepo, !shape.scannerIsGitRepo].map((isGitRepo) => ({
    shape,
    isGitRepo,
    label: isGitRepo === shape.scannerIsGitRepo ? 'current scan' : 'stale cache',
  }))
)

describe('GitStatus against the backend directory shapes (agent-os-ygqe)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it.each(ARMS)(
    '$shape.name, isGitRepo=$isGitRepo ($label): asks, then renders the answer',
    async ({ shape, isGitRepo }) => {
      mockStatus.mockImplementation(shape.answer)
      const stack = stackFor(isGitRepo)
      const { container, queryClient } = renderWithProviders(<GitStatus stack={stack} />)

      await waitFor(() => expect(mockStatus).toHaveBeenCalledWith(stack.id))

      // Settled, not merely requested: "renders nothing" is also what loading
      // renders, so asserting it before the query settles proves nothing.
      await waitFor(() => {
        const status = queryClient.getQueryState(queryKeys.git.all(stack.id))?.status
        expect(status === 'success' || status === 'error').toBe(true)
      })
      shape.expectRender(container)
    }
  )
})
