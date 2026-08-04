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
 * is only reachable via the codemirror Ctrl+S hook (onSave) or the "Save
 * anyway" button in the confirm dialog, so these tests trigger it by invoking
 * the captured onSave callback directly, mirroring the withSelection pattern
 * already used in ComposeEditor.b4.test.tsx for onSelect.
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
}))
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/lint', () => ({ linter: () => [], lintGutter: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))
vi.mock('@/stores/uiStore', () => ({ useUIStore: () => ({ theme: 'light' }) }))

const mockApiGet = vi.fn()
const mockApiPut = vi.fn()
const mockApiPost = vi.fn()

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: (...args: unknown[]) => mockApiGet(...args),
    put: (...args: unknown[]) => mockApiPut(...args),
    post: (...args: unknown[]) => mockApiPost(...args),
  },
  stacksApi: {
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
    mockApiGet.mockResolvedValue({ data: 'services:\n  web:\n    image: nginx\n' })
  })

  it('lints before saving and saves directly when no errors are found', async () => {
    mockApiPost.mockResolvedValue({ data: { lintResults: [] } })
    mockApiPut.mockResolvedValue({ data: { lintResults: [] } })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())

    await triggerCtrlSSave()

    await waitFor(() =>
      expect(mockApiPost).toHaveBeenCalledWith(
        '/stacks/test-stack/compose/lint',
        { content: 'services:\n  web:\n    image: nginx\n' },
      ),
    )
    await waitFor(() =>
      expect(mockApiPut).toHaveBeenCalledWith(
        '/stacks/test-stack/compose',
        { content: 'services:\n  web:\n    image: nginx\n' },
      ),
    )
    expect(screen.queryByText('Save with Lint Errors?')).not.toBeInTheDocument()
    expect(toast.success).toHaveBeenCalledWith('Compose file saved successfully')
  })

  it('shows the save-confirm dialog instead of saving when lint finds errors', async () => {
    mockApiPost.mockResolvedValue({
      data: { lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] },
    })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())

    await triggerCtrlSSave()

    await waitFor(() => expect(screen.getByText('Save with Lint Errors?')).toBeInTheDocument())
    expect(
      within(screen.getByRole('dialog')).getByText('Invalid service config'),
    ).toBeInTheDocument()
    expect(mockApiPut).not.toHaveBeenCalled()
  })

  it('"Save anyway" saves despite lint errors and closes the confirm dialog', async () => {
    mockApiPost.mockResolvedValue({
      data: { lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] },
    })
    mockApiPut.mockResolvedValue({ data: { lintResults: [] } })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()
    await waitFor(() => expect(screen.getByText('Save with Lint Errors?')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Save anyway' }))

    await waitFor(() =>
      expect(mockApiPut).toHaveBeenCalledWith(
        '/stacks/test-stack/compose',
        { content: 'services:\n  web:\n    image: nginx\n' },
      ),
    )
    await waitFor(() =>
      expect(screen.queryByText('Save with Lint Errors?')).not.toBeInTheDocument(),
    )
  })

  it('"Fix errors first" closes the confirm dialog without saving', async () => {
    mockApiPost.mockResolvedValue({
      data: { lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] },
    })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()
    await waitFor(() => expect(screen.getByText('Save with Lint Errors?')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Fix errors first' }))

    await waitFor(() =>
      expect(screen.queryByText('Save with Lint Errors?')).not.toBeInTheDocument(),
    )
    expect(mockApiPut).not.toHaveBeenCalled()
  })

  it('falls back to a direct save when the pre-save lint request itself fails', async () => {
    mockApiPost.mockRejectedValue(new Error('network error'))
    mockApiPut.mockResolvedValue({ data: { lintResults: [] } })

    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())

    await triggerCtrlSSave()

    await waitFor(() =>
      expect(mockApiPut).toHaveBeenCalledWith(
        '/stacks/test-stack/compose',
        { content: 'services:\n  web:\n    image: nginx\n' },
      ),
    )
    expect(screen.queryByText('Save with Lint Errors?')).not.toBeInTheDocument()
  })

  it('shows the inline lint panel when the save itself is rejected with details-nested lintResults', async () => {
    mockApiPost.mockResolvedValue({
      data: { lintResults: [{ level: 'error', message: 'Invalid service config', line: 2 }] },
    })
    // Real backend body (compose.go:164-175) as the axios interceptor
    // flattens it (api.ts:116-119): lintResults live under `details`, and
    // there is no `.response` wrapper on the rejected value — not
    // `error.response.data.lintResults` (agent-os-m2x).
    mockApiPut.mockRejectedValue({
      code: 'COMPOSE_VALIDATION_ERROR',
      message: 'Compose file validation failed',
      details: {
        saved: false,
        lintResults: [{ level: 'error', message: 'Service key must be a string', line: 3, rule: 'service-key' }],
      },
      status: 422,
    })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(capturedOnSave).toBeDefined())
    await triggerCtrlSSave()
    await waitFor(() => expect(screen.getByText('Save with Lint Errors?')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Save anyway' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Lint errors detected'))
    await waitFor(() => expect(screen.getByText('Service key must be a string')).toBeInTheDocument())
  })

  it('clicking Lint runs the lint mutation and toasts success when clean', async () => {
    mockApiPost.mockResolvedValue({ data: { lintResults: [] } })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))

    await waitFor(() =>
      expect(mockApiPost).toHaveBeenCalledWith(
        '/stacks/test-stack/compose/lint',
        { content: 'services:\n  web:\n    image: nginx\n' },
      ),
    )
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('No lint issues found'))
  })

  it('clicking Lint toasts a warning when only warnings are found', async () => {
    mockApiPost.mockResolvedValue({
      data: { lintResults: [{ level: 'warning', message: 'Consider pinning image tag' }] },
    })

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
