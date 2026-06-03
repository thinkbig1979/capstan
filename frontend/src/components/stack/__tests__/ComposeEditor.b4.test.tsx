/**
 * B4 tests for ComposeEditor — extract-to-env atomicity (finding #11).
 *
 * Drives the real confirmExtract function end-to-end via the dialog UI,
 * then asserts on API calls to prove:
 *   - success path: only updateComposeAndEnv is called once (atomic, correct body)
 *   - 404 fallback path: env PUT fires BEFORE compose PUT (safe ordering)
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { useRef } from 'react'

// ─── Shared mock state that tests mutate before rendering ─────────────────────

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

// ─── Mock useCodeMirrorEditor ────────────────────────────────────────────────
//
// Default implementation: returns a viewRef with a stable mock view and does NOT
// fire onSelect (so selectedText stays empty — button stays disabled by default).
//
// Per-test variants use mockImplementationOnce to also fire onSelect('nginx'),
// enabling the Extract button for that specific test.

vi.mock('@/hooks/useCodeMirrorEditor', () => ({
  useCodeMirrorEditor: vi.fn((_ref: unknown, _opts: unknown) => {
     
    const viewRef = useRef<{
      state: typeof mockViewState
      dispatch: typeof mockDispatch
      destroy: () => void
    } | null>(null)
    viewRef.current = { state: mockViewState, dispatch: mockDispatch, destroy: vi.fn() }
    return { viewRef, isDark: false }
  }),
}))

// ─── Other CodeMirror imports (prevent module resolution errors) ──────────────

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

// ─── API mocks ───────────────────────────────────────────────────────────────

const mockApiGet = vi.fn()
const mockApiPut = vi.fn()
const mockApiPost = vi.fn()
const mockGetEnv = vi.fn()
const mockUpdateComposeAndEnv = vi.fn()

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: (...args: unknown[]) => mockApiGet(...args),
    put: (...args: unknown[]) => mockApiPut(...args),
    post: (...args: unknown[]) => mockApiPost(...args),
  },
  stacksApi: {
    getEnv: (...args: unknown[]) => mockGetEnv(...args),
    updateComposeAndEnv: (...args: unknown[]) => mockUpdateComposeAndEnv(...args),
    updateEnv: vi.fn(),
    createEnv: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

// ─── Imports that must come after mocks ──────────────────────────────────────

import { ComposeEditor } from '../ComposeEditor'
import { toast } from 'sonner'

// ─── Helpers ─────────────────────────────────────────────────────────────────

/**
 * Install a one-shot mock on useCodeMirrorEditor that also fires onSelect(text)
 * synchronously during component mount. This causes selectedText to be set so
 * the Extract button becomes enabled.
 */
async function withSelection(selectedValue: string) {
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
    opts.onSelect?.(selectedValue)
    return { viewRef: vRef, isDark: false }
  }
  vi.mocked(mod.useCodeMirrorEditor).mockImplementationOnce(impl)
}

// ─── Suite ───────────────────────────────────────────────────────────────────

describe('ComposeEditor — extract-to-env atomicity (B4 finding #11)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockDispatch.mockReset()
    mockApiGet.mockResolvedValue({ data: 'services:\n  web:\n    image: nginx\n' })
  })

  // ── Baseline ─────────────────────────────────────────────────────────────

  it('renders Save and Lint buttons after loading', async () => {
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
      expect(screen.getByText('Lint')).toBeInTheDocument()
    })
  })

  it('Extract button is disabled with no selection', async () => {
    renderWithProviders(<ComposeEditor stackId="test-stack" />)
    await waitFor(() =>
      expect(screen.getByTitle(/Select a value in the editor/)).toBeDisabled(),
    )
  })

  // ── Atomic success: one request, correct body ─────────────────────────────

  it('atomic: updateComposeAndEnv called once with (id, composeContent, envRaw) and no sequential puts', async () => {
    await withSelection('nginx')
    mockGetEnv.mockResolvedValue({ filename: '.env', raw: 'EXISTING=1\n', entries: [] })
    mockUpdateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'compose and env saved' })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    // Wait for Extract button to be enabled (selection fired)
    await waitFor(() =>
      expect(screen.getByTitle(/Extract selected value to .env file/)).not.toBeDisabled(),
    )

    // Open dialog
    await user.click(screen.getByTitle(/Extract selected value to .env file/))
    await waitFor(() => expect(screen.getByRole('button', { name: /^Extract$/ })).toBeInTheDocument())

    // Confirm extract
    await user.click(screen.getByRole('button', { name: /^Extract$/ }))

    await waitFor(() => expect(mockUpdateComposeAndEnv).toHaveBeenCalledTimes(1))

    // Verify call signature: (stackId, composeContent: string, envRaw: string)
    const [callStackId, callCompose, callEnvRaw] = mockUpdateComposeAndEnv.mock.calls[0] as [
      string,
      string,
      string,
    ]
    expect(callStackId).toBe('test-stack')
    // composeContent must be a string containing the variable placeholder
    expect(typeof callCompose).toBe('string')
    expect(callCompose).toMatch(/\$\{[A-Z_]+\}/) // ${SOMETHING}
    // envRaw must be a flat string (not an object), containing the extracted value
    expect(typeof callEnvRaw).toBe('string')
    expect(callEnvRaw).toContain('nginx')
    // Must also include the existing env content
    expect(callEnvRaw).toContain('EXISTING=1')

    // No sequential writes
    expect(mockApiPut).not.toHaveBeenCalled()

    // Success toast
    expect(toast.success).toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })

  // ── 404 fallback: env PUT before compose PUT ──────────────────────────────

  it('fallback: when atomic 404s, env is written BEFORE compose (safe ordering)', async () => {
    await withSelection('nginx')
    mockGetEnv.mockResolvedValue({ filename: '.env', raw: '', entries: [] })
    mockUpdateComposeAndEnv.mockRejectedValue({ status: 404 })
    // Both sequential puts succeed
    mockApiPut.mockResolvedValue({ data: { saved: true } })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() =>
      expect(screen.getByTitle(/Extract selected value to .env file/)).not.toBeDisabled(),
    )

    await user.click(screen.getByTitle(/Extract selected value to .env file/))
    await waitFor(() => expect(screen.getByRole('button', { name: /^Extract$/ })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /^Extract$/ }))

    await waitFor(() => expect(mockApiPut).toHaveBeenCalledTimes(2))

    // Assert ordering: env URL appears BEFORE compose URL in the call list
    const putUrls = (mockApiPut.mock.calls as Array<[string, ...unknown[]]>).map((c) => c[0])
    const envIdx = putUrls.findIndex((url) => url.includes('/env'))
    const composeIdx = putUrls.findIndex((url) => url.includes('/compose'))

    expect(envIdx).toBeGreaterThanOrEqual(0)
    expect(composeIdx).toBeGreaterThanOrEqual(0)
    // env must come first — the ${VAR} reference is never persisted without its definition
    expect(envIdx).toBeLessThan(composeIdx)

    // The env PUT body must contain the extracted value
    const envPutBody = (mockApiPut.mock.calls[envIdx] as [string, { raw?: string }])[1]
    expect(envPutBody.raw).toContain('nginx')

    expect(toast.success).toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })

  // ── Non-404 error → error toast, no fallback ─────────────────────────────

  it('non-404 atomic error → toast.error, no sequential fallback', async () => {
    await withSelection('nginx')
    mockGetEnv.mockResolvedValue({ filename: '.env', raw: '', entries: [] })
    mockUpdateComposeAndEnv.mockRejectedValue({ status: 500 })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() =>
      expect(screen.getByTitle(/Extract selected value to .env file/)).not.toBeDisabled(),
    )

    await user.click(screen.getByTitle(/Extract selected value to .env file/))
    await waitFor(() => expect(screen.getByRole('button', { name: /^Extract$/ })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /^Extract$/ }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env'),
    )

    expect(mockApiPut).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
  })

  // ── failed ActionResult → error toast with reason ─────────────────────────

  it('failed ActionResult from atomic endpoint → toast.error with backend reason', async () => {
    await withSelection('nginx')
    mockGetEnv.mockResolvedValue({ filename: '.env', raw: '', entries: [] })
    mockUpdateComposeAndEnv.mockResolvedValue({
      outcome: 'failed',
      reason: 'Compose validation failed',
    })

    const user = userEvent.setup()
    renderWithProviders(<ComposeEditor stackId="test-stack" />)

    await waitFor(() =>
      expect(screen.getByTitle(/Extract selected value to .env file/)).not.toBeDisabled(),
    )

    await user.click(screen.getByTitle(/Extract selected value to .env file/))
    await waitFor(() => expect(screen.getByRole('button', { name: /^Extract$/ })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /^Extract$/ }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Compose validation failed'),
    )

    expect(mockApiPut).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
  })
})
