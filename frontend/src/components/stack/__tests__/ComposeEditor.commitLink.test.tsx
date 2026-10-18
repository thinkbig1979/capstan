/**
 * The save toast's "Commit and push" link (agent-os-k81p.3). Saving and the
 * toast make no git call; the link only opens the dialog, and the dialog's
 * first request is a read. Every git endpoint is a mock, so the call log is
 * the whole truth about what was sent.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'
import { useRef } from 'react'
import { QueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/query-keys'

const mockViewState = {
  doc: {
    toString: () => 'services:\n  web:\n    image: nginx\n',
    length: 40,
  },
  selection: {
    main: { from: 31, to: 36 }, // covers "nginx"
  },
}
const mockDispatch = vi.fn()

// Captured from the last useCodeMirrorEditor call so tests can simulate the
// codemirror Ctrl+S keymap firing (real onSave is wired inside the codemirror
// extension under test elsewhere; here we invoke it directly).
let capturedOnSave: (() => boolean) | undefined

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn((_ref: unknown, opts: { onSave?: () => boolean }) => {

    const viewRef = useRef<{
      state: typeof mockViewState
      dispatch: typeof mockDispatch
      destroy: () => void
    } | null>(null)
    viewRef.current = { state: mockViewState, dispatch: mockDispatch, destroy: vi.fn() }
    capturedOnSave = opts.onSave
    return { viewRef, isDark: false }
  }),
}))

vi.mock('codemirror', () => ({ basicSetup: [], EditorView: class { destroy() {} } }))
vi.mock('@codemirror/state', () => ({ EditorState: { create: () => ({}) } }))
vi.mock('@codemirror/view', () => ({
  EditorView: class { destroy() {} },
  keymap: { of: () => [] },
  ViewPlugin: { define: () => [] },
}))
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/lint', () => ({ linter: () => [], lintGutter: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))
vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

const mockUpdateCompose = vi.fn()
const git = {
  status: vi.fn(),
  changes: vi.fn(),
  changesDiff: vi.fn(),
  commit: vi.fn(),
  push: vi.fn(),
  pull: vi.fn(),
  log: vi.fn(),
  diff: vi.fn(),
}

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: vi.fn().mockResolvedValue('services:\n  web:\n    image: nginx\n'),
    updateCompose: (...args: unknown[]) => mockUpdateCompose(...args),
    lintCompose: vi.fn().mockResolvedValue({ lintResults: [] }),
    get: vi.fn(),
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
  gitApi: {
    status: (...a: unknown[]) => git.status(...a),
    changes: (...a: unknown[]) => git.changes(...a),
    changesDiff: (...a: unknown[]) => git.changesDiff(...a),
    commit: (...a: unknown[]) => git.commit(...a),
    push: (...a: unknown[]) => git.push(...a),
    pull: (...a: unknown[]) => git.pull(...a),
    log: (...a: unknown[]) => git.log(...a),
    diff: (...a: unknown[]) => git.diff(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ComposeEditor } from '../ComposeEditor'
import { toast } from 'sonner'

/** Every git endpoint called so far, by name. */
const gitCalls = () => Object.entries(git).filter(([, fn]) => fn.mock.calls.length > 0).map(([name]) => name)

function renderEditor(isGitRepo: boolean) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
  // What StackPage has already fetched.
  queryClient.setQueryData(queryKeys.stack.detail('s1'), {
    id: 's1', directory: '/stacks/app', composeFile: 'compose.yaml', envFile: '.env', projectName: 'app', isGitRepo,
  })
  return renderWithProviders(<ComposeEditor stackId="s1" />, { queryClient })
}

async function save() {
  await waitFor(() => expect(capturedOnSave).toBeDefined())
  await act(async () => {
    capturedOnSave?.()
  })
  await waitFor(() => expect(toast.success).toHaveBeenCalled())
}

describe('ComposeEditor: the save toast\'s Commit and push link', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    capturedOnSave = undefined
    mockUpdateCompose.mockResolvedValue({ lintResults: [] })
    git.changes.mockResolvedValue({
      branch: 'main', detached: false, trackingBranch: 'origin/main', hasUpstream: true, ahead: 0, behind: 0,
      pushEnabled: true, identityConfigured: true, changes: [{ path: 'compose.yaml', status: '.M', isEnv: false }],
    })
    git.changesDiff.mockResolvedValue({ path: 'compose.yaml', hidden: false, diff: '' })
  })

  it('a git stack\'s toast carries the link; save and toast call no git endpoint; the click opens the dialog, which only reads', async () => {
    renderEditor(true)
    await save()
    expect(mockUpdateCompose).toHaveBeenCalledTimes(1)

    const [title, opts] = vi.mocked(toast.success).mock.calls[0] as [string, { action?: { label: string; onClick: () => void } }]
    expect(title).toBe('Compose file saved successfully')
    expect(opts.action?.label).toBe('Commit and push')
    expect(gitCalls()).toEqual([])
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    await act(async () => {
      opts.action?.onClick()
    })
    expect(await screen.findByRole('dialog', { name: 'Commit and push' })).toBeInTheDocument()
    await waitFor(() => expect(git.changes).toHaveBeenCalledWith('s1'))
    // The compose file is pre-ticked and its diff is fetched: reads only.
    await waitFor(() => expect(git.changesDiff).toHaveBeenCalledWith('s1', 'compose.yaml'))
    expect(gitCalls().sort()).toEqual(['changes', 'changesDiff'])
    expect(git.commit).not.toHaveBeenCalled()
    expect(git.push).not.toHaveBeenCalled()
  })

  it('a stack that is not a git repository gets the plain toast and no link', async () => {
    renderEditor(false)
    await save()
    expect(vi.mocked(toast.success).mock.calls[0]).toEqual(['Compose file saved successfully'])
    expect(gitCalls()).toEqual([])
  })
})
