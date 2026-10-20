/**
 * Gap-filling tests for ComposeEditor's save/lint decision logic — written
 * ahead of the compose-editor/ split (see ComposeEditor.test.tsx and
 * ComposeEditor.b4.test.tsx for pre-existing coverage of rendering and the
 * extract-to-env atomic write).
 *
 * None of the pre-existing suites drive handleSave's lint-before-save branch,
 * the save-with-errors confirm dialog, the Lint button, or the inferVarName
 * pre-fill — this file closes those gaps so the split can be checked for
 * behavior-preservation against a real baseline.
 *
 * The Save button in the toolbar is unconditionally disabled by the
 * hasUnsavedChanges quirk (content and lastSaved are always written together,
 * so hasUnsavedChanges never becomes true) — see ComposeEditor.tsx. handleSave
 * is only reachable via the codemirror Ctrl+S hook (onSave), so these tests
 * trigger it by invoking the captured onSave callback directly, mirroring the
 * withSelection pattern already used in ComposeEditor.b4.test.tsx for onSelect.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { useRef } from 'react'

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

const mockGetCompose = vi.fn()
const mockUpdateCompose = vi.fn()
const mockLintCompose = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    getCompose: (...args: unknown[]) => mockGetCompose(...args),
    updateCompose: (...args: unknown[]) => mockUpdateCompose(...args),
    lintCompose: (...args: unknown[]) => mockLintCompose(...args),
    getEnv: vi.fn(),
    updateComposeAndEnv: vi.fn(),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ComposeEditor } from '../ComposeEditor'
import { toast } from 'sonner'

async function triggerCtrlSSave() {
  await act(async () => {
    capturedOnSave?.()
  })
}

describe('ComposeEditor — save/lint decision logic', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockDispatch.mockReset()
    capturedOnSave = undefined
    mockGetCompose.mockResolvedValue('services:\n  web:\n    image: nginx\n')
  })

  it('lints before saving and saves directly when no errors are found', async () => {
    mockLintCompose.mockResolvedValue({ lintResults: [] })
    mockUpdateCompose.mockResolvedValue({ lintResults: [] })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())

    await triggerCtrlSSave()

    await waitFor(() =>
      expect(mockLintCompose).toHaveBeenCalledWith(
        'test-stack',
        'services:\n  web:\n    image: nginx\n',
      ),
    )
    await waitFor(() =>
      expect(mockUpdateCompose).toHaveBeenCalledWith(
        'test-stack',
        'services:\n  web:\n    image: nginx\n',
      ),
    )
    expect(screen.queryByText('Fix Lint Errors Before Saving')).not.toBeInTheDocument()
    expect(toast.success).toHaveBeenCalledWith('Compose file saved successfully')
  })

  it('shows the save-confirm dialog instead of saving when lint finds errors', async () => {
    mockLintCompose.mockResolvedValue({ lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())

    await triggerCtrlSSave()

    await waitFor(() => expect(screen.getByText('Fix Lint Errors Before Saving')).toBeInTheDocument())
    expect(
      within(screen.getByRole('dialog')).getByText('Invalid service config'),
    ).toBeInTheDocument()
    expect(mockUpdateCompose).not.toHaveBeenCalled()
  })

  it('offers no "Save anyway" for lint errors: the dialog says fix them first and nothing is saved', async () => {
    // The server answers 422 and writes nothing for error-level lint
    // (compose.go), so the dialog must not offer a save it cannot honour
    // (agent-os-p9j8). updateCompose stays unmocked here, so any save attempt
    // would fail the not.toHaveBeenCalled assertion below.
    mockLintCompose.mockResolvedValue({ lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Fix Lint Errors Before Saving')).toBeInTheDocument()
    expect(
      within(dialog).getByText(/A compose file with errors cannot be saved\. Fix them, then save again\./),
    ).toBeInTheDocument()
    expect(within(dialog).getByText('1 error')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /save anyway/i })).not.toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Fix errors first' })).toBeInTheDocument()
    expect(mockUpdateCompose).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('pluralises the error count in the dialog', async () => {
    mockLintCompose.mockResolvedValue({
      lintResults: [
        { level: 'error', message: 'First problem', line: 1 },
        { level: 'error', message: 'Second problem', line: 2 },
        { level: 'warning', message: 'Not counted' },
      ],
    })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('2 errors')).toBeInTheDocument()
  })

  it('saves a file with only lint warnings, with no dialog', async () => {
    mockLintCompose.mockResolvedValue({ lintResults: [{ level: 'warning', message: 'Consider pinning image tag' }] })
    mockUpdateCompose.mockResolvedValue({ saved: true, lintResults: [{ level: 'warning', message: 'Consider pinning image tag' }] })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()

    await waitFor(() => expect(mockUpdateCompose).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(toast.success).toHaveBeenCalledTimes(1))
  })

  it('"Fix errors first" closes the confirm dialog without saving', async () => {
    mockLintCompose.mockResolvedValue({ lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()
    await waitFor(() => expect(screen.getByText('Fix Lint Errors Before Saving')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Fix errors first' }))

    await waitFor(() =>
      expect(screen.queryByText('Fix Lint Errors Before Saving')).not.toBeInTheDocument(),
    )
    expect(mockUpdateCompose).not.toHaveBeenCalled()
  })

  it('falls back to a direct save when the pre-save lint request itself fails', async () => {
    mockLintCompose.mockRejectedValue(new Error('network error'))
    mockUpdateCompose.mockResolvedValue({ lintResults: [] })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())

    await triggerCtrlSSave()

    await waitFor(() =>
      expect(mockUpdateCompose).toHaveBeenCalledWith(
        'test-stack',
        'services:\n  web:\n    image: nginx\n',
      ),
    )
    expect(screen.queryByText('Fix Lint Errors Before Saving')).not.toBeInTheDocument()
  })

  it('a 422 from the server leaves nothing claimed as saved and shows its lint results inline', async () => {
    // The pre-save lint request fails, so the save goes straight to the
    // server, which is the one route to a 422 now that the dialog has no
    // save button.
    mockLintCompose.mockRejectedValue(new Error('network error'))
    // Real backend body (compose.go:164-175) as the axios interceptor
    // flattens it (api.ts:116-119): lintResults live under `details`, and
    // there is no `.response` wrapper on the rejected value — not
    // `error.response.data.lintResults` (agent-os-m2x).
    mockUpdateCompose.mockRejectedValue({
      code: 'COMPOSE_VALIDATION_ERROR',
      message: 'Compose file validation failed',
      details: {
        saved: false,
        lintResults: [{ level: 'error', message: 'Service key must be a string', line: 3, rule: 'service-key' }],
      },
      status: 422,
    })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Lint errors detected'))
    await waitFor(() => expect(screen.getByText('Service key must be a string')).toBeInTheDocument())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('clicking Lint runs the lint mutation and toasts success when clean', async () => {
    mockLintCompose.mockResolvedValue({ lintResults: [] })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))

    await waitFor(() =>
      expect(mockLintCompose).toHaveBeenCalledWith(
        'test-stack',
        'services:\n  web:\n    image: nginx\n',
      ),
    )
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('No lint issues found'))
  })

  it('clicking Lint toasts a warning when only warnings are found', async () => {
    mockLintCompose.mockResolvedValue({ lintResults: [{ level: 'warning', message: 'Consider pinning image tag' }] })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))

    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('Lint warnings detected'))
  })

  it('pre-fills the extract-to-env variable name from the YAML key above the selection', async () => {
    const mod = await import('@/hooks/useCodeMirrorEditor')

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const impl: any = (_ref: unknown, opts: { onSelect?: (t: string) => void }) => {
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const vRef = useRef<{
        state: typeof mockViewState
        dispatch: typeof mockDispatch
        destroy: () => void
      } | null>(null)
      vRef.current = { state: mockViewState, dispatch: mockDispatch, destroy: vi.fn() }
      opts.onSelect?.('nginx')
      return { viewRef: vRef, isDark: false }
    }
    vi.mocked(mod.useCodeMirrorEditor).mockImplementationOnce(impl)

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() =>
      expect(screen.getByTitle(/Extract selected value to .env file/)).not.toBeDisabled(),
    )
    await user.click(screen.getByTitle(/Extract selected value to .env file/))

    await waitFor(() => {
      const input = screen.getByLabelText('Variable name') as HTMLInputElement
      expect(input.value).toBe('IMAGE')
    })
  })
})
