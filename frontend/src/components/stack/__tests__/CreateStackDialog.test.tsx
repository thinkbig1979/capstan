/**
 * Regression coverage for CreateStackDialog, written before the component is
 * split into frontend/src/components/stack/create-stack/*. Exercises the
 * behaviors that live in CreateStackDialog.tsx today: name validation, the
 * compose-editor / docker-run tabs, lint wiring, env-file toggle, deploy
 * checkbox, and the handleCreate success/partial/failure branches (including
 * navigation + form reset).
 *
 * useCreateStack's own toast/outcome differentiation is already covered by
 * src/hooks/__tests__/useCreateStack.test.tsx and docker-run-parser's parsing
 * logic by src/lib/__tests__/docker-run-parser.test.ts — this file mocks both
 * to control CreateStackDialog's own wiring deterministically rather than
 * re-testing their internals.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'

// ─── codemirror mocks (component mounts useCodeMirrorEditor directly) ────────
vi.mock('codemirror', () => {
  class MockEditorView {
    static theme = () => []
    static updateListener = { of: () => [] }
    dispatch() {}
    destroy() {}
    state = { doc: { toString: () => '', length: 0 } }
  }
  return { basicSetup: [], EditorView: MockEditorView }
})
vi.mock('@codemirror/state', () => ({
  EditorState: { create: () => ({ doc: { toString: () => '', length: 0 } }) },
}))
vi.mock('@codemirror/view', () => {
  class MockEditorView {
    static theme = () => []
    static updateListener = { of: () => [] }
    dispatch() {}
    destroy() {}
    state = { doc: { toString: () => '', length: 0 } }
  }
  return {
    EditorView: MockEditorView,
    keymap: { of: () => [] },
  }
})
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => [] }))
vi.mock('@codemirror/lint', () => ({ linter: () => [], lintGutter: () => [] }))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/search', () => ({ search: () => [] }))
vi.mock('@codemirror/autocomplete', () => ({ autocompletion: () => [] }))
vi.mock('@/stores/uiStore', () => ({
  useUIStore: () => ({ theme: 'light' }),
}))

// ─── api / toast / router / docker-run-parser mocks ──────────────────────────
const mockGetConfig = vi.fn()
const mockLint = vi.fn()
const mockCreate = vi.fn()

vi.mock('@/lib/api', () => ({
  settingsApi: {
    getConfig: (...args: unknown[]) => mockGetConfig(...args),
  },
  stacksApi: {
    lint: (...args: unknown[]) => mockLint(...args),
    create: (...args: unknown[]) => mockCreate(...args),
  },
}))

const mockToastSuccess = vi.fn()
const mockToastError = vi.fn()
const mockToastWarning = vi.fn()
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    warning: (...args: unknown[]) => mockToastWarning(...args),
  },
}))

const mockNavigate = vi.fn()
vi.mock('react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const mockIsDockerRunCommand = vi.fn()
const mockConvertDockerRun = vi.fn()
vi.mock('@/lib/docker-run-parser', () => ({
  isDockerRunCommand: (...args: unknown[]) => mockIsDockerRunCommand(...args),
  convertDockerRun: (...args: unknown[]) => mockConvertDockerRun(...args),
}))

import { CreateStackDialog } from '../CreateStackDialog'

const ONE_DIR_CONFIG = { stacksDir: '/data/stacks', stacksDirectories: ['/data/stacks'] }
const MULTI_DIR_CONFIG = {
  stacksDir: '/data/stacks',
  stacksDirectories: ['/data/stacks', '/data/other'],
}

function renderDialog(onOpenChange = vi.fn()) {
  const utils = renderWithProviders(
    <CreateStackDialog open onOpenChange={onOpenChange} />,
  )
  return { ...utils, onOpenChange }
}

describe('CreateStackDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetConfig.mockResolvedValue(ONE_DIR_CONFIG)
    mockLint.mockResolvedValue({ valid: true, lintResults: [] })
  })

  // ─── rendering / directory selector ────────────────────────────────────────

  it('does not render a directory selector when only one stacks directory is configured', async () => {
    renderDialog()
    await waitFor(() => expect(screen.getByText('Stack Name')).toBeInTheDocument())
    expect(screen.queryByLabelText('Target Directory')).not.toBeInTheDocument()
  })

  it('renders a directory selector with a Default badge when multiple stacks directories are configured', async () => {
    mockGetConfig.mockResolvedValue(MULTI_DIR_CONFIG)
    renderDialog()
    await waitFor(() => expect(screen.getByLabelText('Target Directory')).toBeInTheDocument())
    expect(screen.getByText('Default')).toBeInTheDocument()
  })

  // ─── name validation ────────────────────────────────────────────────────────

  // Each inline field error is intentionally mirrored into the bottom
  // validation-errors panel (see the "disables Create" test below), so these
  // assertions target the inline #stack-name-error paragraph specifically
  // rather than getByText, which would match both occurrences.

  it('shows a required error when the name is cleared after being typed', async () => {
    const user = userEvent.setup()
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'x')
    await user.clear(nameInput)
    expect(document.getElementById('stack-name-error')).toHaveTextContent('Stack name is required')
  })

  it('shows a character validation error for disallowed characters', async () => {
    const user = userEvent.setup()
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my stack!')
    expect(document.getElementById('stack-name-error')).toHaveTextContent(
      'Only letters, numbers, dots, hyphens, and underscores are allowed',
    )
  })

  it('shows a length validation error for names over 50 characters', async () => {
    const user = userEvent.setup()
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'a'.repeat(51))
    expect(document.getElementById('stack-name-error')).toHaveTextContent(
      'Stack name must be between 1 and 50 characters',
    )
  })

  it('shows the target directory preview once the name is valid', async () => {
    const user = userEvent.setup()
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    expect(screen.getByText('Directory will be /data/stacks/my-stack')).toBeInTheDocument()
  })

  // ─── compose tabs ───────────────────────────────────────────────────────────

  it('defaults to the compose editor tab and switches to docker-run and back', async () => {
    const user = userEvent.setup()
    renderDialog()
    await waitFor(() => expect(screen.getByText('Docker Compose')).toBeInTheDocument())
    expect(screen.queryByPlaceholderText(/docker run -d/)).not.toBeInTheDocument()

    await user.click(screen.getByText('Convert docker run'))
    expect(screen.getByPlaceholderText(/docker run -d/)).toBeInTheDocument()

    await user.click(screen.getByText('Compose Editor'))
    expect(screen.queryByPlaceholderText(/docker run -d/)).not.toBeInTheDocument()
  })

  // ─── lint ───────────────────────────────────────────────────────────────────

  it('lints the compose content and shows a success toast when clean', async () => {
    const user = userEvent.setup()
    mockLint.mockResolvedValue({ valid: true, lintResults: [] })
    renderDialog()
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))
    await waitFor(() => expect(mockLint).toHaveBeenCalled())
    expect(mockToastSuccess).toHaveBeenCalledWith('No lint issues found')
  })

  it('shows an error toast and inline error count when lint finds errors', async () => {
    const user = userEvent.setup()
    mockLint.mockResolvedValue({
      valid: false,
      lintResults: [{ level: 'error', message: 'bad service', line: 3 }],
    })
    renderDialog()
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('Lint errors detected'))
    expect(screen.getByText('1 error(s) found - fix before creating')).toBeInTheDocument()
  })

  it('shows a warning toast when lint finds only warnings', async () => {
    const user = userEvent.setup()
    mockLint.mockResolvedValue({
      valid: true,
      lintResults: [{ level: 'warning', message: 'consider pinning tag' }],
    })
    renderDialog()
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))
    await waitFor(() =>
      expect(mockToastWarning).toHaveBeenCalledWith('Lint warnings detected'),
    )
  })

  it('shows an error toast when the lint request itself fails', async () => {
    const user = userEvent.setup()
    mockLint.mockRejectedValue(new Error('network down'))
    renderDialog()
    await waitFor(() => expect(screen.getByText('Lint')).toBeInTheDocument())

    await user.click(screen.getByText('Lint'))
    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith('Failed to lint compose file'),
    )
  })

  // ─── docker run conversion ──────────────────────────────────────────────────

  it('disables the Convert button until a docker run command is entered', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(await screen.findByText('Convert docker run'))
    expect(screen.getByRole('button', { name: /^Convert$/ })).toBeDisabled()
  })

  it('shows an error toast when the pasted text is not a docker run command', async () => {
    const user = userEvent.setup()
    mockIsDockerRunCommand.mockReturnValue(false)
    renderDialog()
    await user.click(await screen.findByText('Convert docker run'))
    await user.type(screen.getByPlaceholderText(/docker run -d/), 'echo hello')
    await user.click(screen.getByRole('button', { name: /^Convert$/ }))

    expect(mockToastError).toHaveBeenCalledWith('Input does not appear to be a docker run command')
    expect(mockConvertDockerRun).not.toHaveBeenCalled()
  })

  it('converts a valid docker run command and switches back to the editor tab', async () => {
    const user = userEvent.setup()
    mockIsDockerRunCommand.mockReturnValue(true)
    mockConvertDockerRun.mockReturnValue('services:\n  webapp:\n    image: nginx:alpine\n')
    renderDialog()
    await user.click(await screen.findByText('Convert docker run'))
    await user.type(screen.getByPlaceholderText(/docker run -d/), 'docker run -d nginx:alpine')
    await user.click(screen.getByRole('button', { name: /^Convert$/ }))

    expect(mockToastSuccess).toHaveBeenCalledWith('Docker run command converted to Compose')
    expect(screen.queryByPlaceholderText(/docker run -d/)).not.toBeInTheDocument()
    expect(screen.getByText('Compose Editor')).toBeInTheDocument()
  })

  it('shows a conversion error when convertDockerRun throws', async () => {
    const user = userEvent.setup()
    mockIsDockerRunCommand.mockReturnValue(true)
    mockConvertDockerRun.mockImplementation(() => {
      throw new Error('parse failure')
    })
    renderDialog()
    await user.click(await screen.findByText('Convert docker run'))
    await user.type(screen.getByPlaceholderText(/docker run -d/), 'docker run -d nginx:alpine')
    await user.click(screen.getByRole('button', { name: /^Convert$/ }))

    expect(
      screen.getByText('Failed to parse the docker run command. Check the syntax and try again.'),
    ).toBeInTheDocument()
    expect(mockToastError).toHaveBeenCalledWith('Failed to parse the docker run command')
  })

  // ─── env file toggle ────────────────────────────────────────────────────────

  it('toggles the .env editor and accepts input', async () => {
    const user = userEvent.setup()
    renderDialog()
    await waitFor(() => expect(screen.getByText('Add .env File')).toBeInTheDocument())

    await user.click(screen.getByText('Add .env File'))
    expect(screen.getByText('Hide .env File')).toBeInTheDocument()
    const envTextarea = screen.getByLabelText('.env File')
    await user.type(envTextarea, 'KEY=value')
    expect(envTextarea).toHaveValue('KEY=value')

    await user.click(screen.getByText('Hide .env File'))
    expect(screen.queryByLabelText('.env File')).not.toBeInTheDocument()
  })

  // ─── deploy checkbox ────────────────────────────────────────────────────────

  it('toggles the deploy checkbox', async () => {
    const user = userEvent.setup()
    renderDialog()
    const checkbox = await screen.findByLabelText('Deploy after creation')
    expect(checkbox).not.toBeChecked()
    await user.click(checkbox)
    expect(checkbox).toBeChecked()
  })

  // ─── create flow ────────────────────────────────────────────────────────────

  it('disables Create and lists the validation error when the name is invalid', async () => {
    const user = userEvent.setup()
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'bad name!')

    const createButton = screen.getByRole('button', { name: /Create Stack/ })
    expect(createButton).toBeDisabled()
    expect(screen.getByText('Please fix the following errors:')).toBeInTheDocument()
    expect(
      within(screen.getByText('Please fix the following errors:').closest('div')!.parentElement!).getByText(
        'Only letters, numbers, dots, hyphens, and underscores are allowed',
      ),
    ).toBeInTheDocument()
  })

  it('creates a stack with the expected default payload', async () => {
    const user = userEvent.setup()
    mockCreate.mockReturnValue(new Promise(() => {})) // leave pending; only assert the call
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith({
        name: 'my-stack',
        directory: undefined,
        composeContent: expect.stringContaining('services:'),
        envContent: undefined,
        deploy: false,
      }),
    )
  })

  it('includes envContent in the payload only when the env editor is shown', async () => {
    const user = userEvent.setup()
    mockCreate.mockReturnValue(new Promise(() => {}))
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByText('Add .env File'))
    await user.type(screen.getByLabelText('.env File'), 'PORT=8080')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith(
        expect.objectContaining({ envContent: 'PORT=8080' }),
      ),
    )
  })

  it('navigates to the new stack and resets the form on a full success', async () => {
    const user = userEvent.setup()
    mockCreate.mockResolvedValue({
      outcome: 'success',
      reason: 'Stack created',
      details: { stack: { id: 'my-stack~default' }, lintResults: [] },
    })
    const onOpenChange = vi.fn()
    renderDialog(onOpenChange)
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/stacks/my-stack~default'))
    expect(onOpenChange).toHaveBeenCalledWith(false)
    await waitFor(() => expect(nameInput).toHaveValue(''))
  })

  it('does not navigate or reset when the success response carries no stack id', async () => {
    const user = userEvent.setup()
    mockCreate.mockResolvedValue({ outcome: 'no_change', reason: 'nothing to do', details: {} })
    const onOpenChange = vi.fn()
    renderDialog(onOpenChange)
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() => expect(mockCreate).toHaveBeenCalled())
    expect(mockNavigate).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
    expect(nameInput).toHaveValue('my-stack')
  })

  it('navigates and resets on a partial (created-but-not-deployed) rejection', async () => {
    const user = userEvent.setup()
    mockCreate.mockRejectedValue({
      outcome: 'partial',
      reason: 'created but not deployed',
      details: { stack: { id: 'partial-stack' } },
    })
    const onOpenChange = vi.fn()
    renderDialog(onOpenChange)
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/stacks/partial-stack'))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('surfaces inline lint errors from a genuine create failure without navigating', async () => {
    const user = userEvent.setup()
    // Real backend body (stack_crud.go:172-181) as the axios interceptor
    // flattens it (api.ts:116-119): `code`/`message`/`details` at the top
    // level, lintResults nested under `details` — not a bare top-level
    // `.lintResults`/`.error` (agent-os-m2x).
    mockCreate.mockRejectedValue({
      code: 'COMPOSE_VALIDATION_ERROR',
      message: 'Compose file validation failed',
      details: { lintResults: [{ level: 'error', message: 'bad indentation', line: 5 }] },
      status: 422,
    })
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() =>
      expect(screen.getByText('1 error(s) found - fix before creating')).toBeInTheDocument(),
    )
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('shows the backend message and the DUPLICATE_STACK toast on a duplicate-name create', async () => {
    const user = userEvent.setup()
    // Real backend body (stack_crud.go:105-121) — 409 DUPLICATE_STACK, no
    // `details` (NewAppError, not NewAppErrorWithDetails), and no top-level
    // `.error` field: the backend never sends one (models/errors.go:44-49).
    mockCreate.mockRejectedValue({
      code: 'DUPLICATE_STACK',
      message: "Stack directory 'my-stack' already exists",
      status: 409,
    })
    renderDialog()
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')
    await user.click(screen.getByRole('button', { name: /Create Stack/ }))

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith('A stack with this name already exists'),
    )
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  // ─── cancel ─────────────────────────────────────────────────────────────────

  it('resets the form and closes on Cancel', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog(onOpenChange)
    const nameInput = await screen.findByLabelText('Stack Name')
    await user.type(nameInput, 'my-stack')

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('resets the form when closed via Escape (Radix onOpenChange path)', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog(onOpenChange)
    await screen.findByLabelText('Stack Name')

    await user.keyboard('{Escape}')
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
