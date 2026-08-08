import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { CreateNetworkDialog } from '../CreateNetworkDialog'

/**
 * NetworksTab is this dialog's only parent, and NetworksTab.test.tsx stubs it.
 * Criterion 5 of agent-os-m1mu: a child that is stubbed in its parent's test
 * needs its own direct test — stub OR test directly, never neither. This is
 * that direct test (it was 0/36 statements).
 */

const mockCreateNetwork = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    createNetwork: (...a: unknown[]) => mockCreateNetwork(...a),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

beforeEach(() => {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
})

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

function renderDialog(open = true) {
  const onOpenChange = vi.fn()
  const result = render(<CreateNetworkDialog open={open} onOpenChange={onOpenChange} />, {
    wrapper: createWrapper(),
  })
  return { ...result, onOpenChange }
}

const INVALID_NAME_MESSAGE =
  'Use letters, digits, "_", ".", "-" (must start with a letter or digit, max 63 chars)'

beforeEach(() => {
  vi.clearAllMocks()
  mockCreateNetwork.mockResolvedValue({})
})

describe('CreateNetworkDialog — rendering', () => {
  it('renders nothing while closed', () => {
    renderDialog(false)

    expect(screen.queryByText('Create Network')).not.toBeInTheDocument()
  })

  it('opens with an empty name and the bridge driver', () => {
    renderDialog()

    expect(screen.getByText('Create Network')).toBeInTheDocument()
    expect(screen.getByLabelText('Name')).toHaveValue('')
    expect(screen.getByText('bridge')).toBeInTheDocument()
    expect(screen.getByLabelText('Internal')).not.toBeChecked()
    expect(screen.getByLabelText('Attachable')).not.toBeChecked()
  })

  it('disables Create until a name is typed', () => {
    renderDialog()

    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
  })
})

describe('CreateNetworkDialog — name validation', () => {
  it('accepts a valid name and enables Create', () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'my-network' } })

    expect(screen.queryByText(INVALID_NAME_MESSAGE)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled()
  })

  it('requires a name once the field has been touched and emptied', () => {
    renderDialog()

    const name = screen.getByLabelText('Name')
    fireEvent.change(name, { target: { value: 'x' } })
    fireEvent.change(name, { target: { value: '' } })

    expect(screen.getByText('Network name is required')).toBeInTheDocument()
    expect(name).toHaveAttribute('aria-invalid', 'true')
  })

  it('treats whitespace as empty', () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '   ' } })

    expect(screen.getByText('Network name is required')).toBeInTheDocument()
  })

  it.each([
    ['-leading-dash', 'must start with a letter or digit'],
    ['has space', 'no spaces allowed'],
    ['bad/slash', 'no slashes allowed'],
  ])('rejects %s', (value) => {
    renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value } })

    expect(screen.getByText(INVALID_NAME_MESSAGE)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
  })

  it('rejects a name longer than 63 characters', () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'a'.repeat(64) } })

    expect(screen.getByText(INVALID_NAME_MESSAGE)).toBeInTheDocument()
  })

  it('accepts a name of exactly 63 characters', () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'a'.repeat(63) } })

    expect(screen.queryByText(INVALID_NAME_MESSAGE)).not.toBeInTheDocument()
  })

  it('points the field at its error message for screen readers', () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '-bad' } })

    expect(screen.getByLabelText('Name')).toHaveAttribute(
      'aria-describedby',
      'network-name-error',
    )
  })
})

describe('CreateNetworkDialog — submitting', () => {
  it('creates a network with the chosen options', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'my-network' } })
    await user.click(screen.getByRole('combobox', { name: 'Driver' }))
    await user.click(await screen.findByRole('option', { name: 'overlay' }))
    fireEvent.click(screen.getByLabelText('Internal'))
    fireEvent.click(screen.getByLabelText('Attachable'))

    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(mockCreateNetwork).toHaveBeenCalledWith({
        name: 'my-network',
        driver: 'overlay',
        internal: true,
        attachable: true,
      }),
    )
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('closes and clears the form on success, so the next open starts fresh', async () => {
    const { onOpenChange, rerender } = renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'my-network' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))

    rerender(<CreateNetworkDialog open onOpenChange={onOpenChange} />)
    expect(screen.getByLabelText('Name')).toHaveValue('')
  })

  it('keeps the dialog open when creation fails', async () => {
    mockCreateNetwork.mockRejectedValue(new Error('boom'))
    const { onOpenChange } = renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'my-network' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(mockCreateNetwork).toHaveBeenCalled())
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
    expect(screen.getByLabelText('Name')).toHaveValue('my-network')
  })

  it('closes without creating anything when Cancel is used', () => {
    const { onOpenChange } = renderDialog()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'my-network' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(mockCreateNetwork).not.toHaveBeenCalled()
  })
})
