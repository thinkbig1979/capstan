import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { LoginForm } from '../LoginForm'

describe('LoginForm', () => {
  it('renders login form with username and password fields', () => {
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} />)

    expect(screen.getByLabelText(/username/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /reveal password/i })).toBeInTheDocument()
  })

  it('toggles password visibility when the reveal button is clicked', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} />)

    const passwordInput = screen.getByLabelText('Password')
    expect(passwordInput).toHaveAttribute('type', 'password')

    await user.click(screen.getByRole('button', { name: /reveal password/i }))
    expect(passwordInput).toHaveAttribute('type', 'text')

    await user.click(screen.getByRole('button', { name: /hide password/i }))
    expect(passwordInput).toHaveAttribute('type', 'password')
  })

  it('displays loading state when isLoading is true', () => {
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} isLoading={true} />)

    expect(screen.getByRole('button', { name: /login/i })).toBeDisabled()
  })

  it('displays error message when provided', () => {
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} error="Invalid credentials" />)

    expect(screen.getByText(/invalid credentials/i)).toBeInTheDocument()
  })

  it('uses custom button text when provided', () => {
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} buttonText="Sign Up" />)

    expect(screen.getByRole('button', { name: /sign up/i })).toBeInTheDocument()
  })

  describe('password complexity (setup)', () => {
    it('blocks a password missing uppercase/number/special when enforceComplexity is set', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn()
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), 'lowercaseonly')
      await user.click(screen.getByRole('button', { name: /create account/i }))

      expect(await screen.findByText(/uppercase letter/i)).toBeInTheDocument()
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('accepts a fully complex password when enforceComplexity is set', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn().mockResolvedValue(undefined)
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), 'Str0ng!pass')
      await user.type(screen.getByLabelText(/confirm password/i), 'Str0ng!pass')
      await user.click(screen.getByRole('button', { name: /create account/i }))

      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    })

    it('renders a confirm-password field only in setup mode', () => {
      const onSubmit = vi.fn()
      const { rerender } = render(<LoginForm onSubmit={onSubmit} />)
      expect(screen.queryByLabelText(/confirm password/i)).not.toBeInTheDocument()

      rerender(<LoginForm onSubmit={onSubmit} enforceComplexity />)
      expect(screen.getByLabelText(/confirm password/i)).toBeInTheDocument()
    })

    it('blocks submit when confirm-password does not match', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn()
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), 'Str0ng!pass')
      await user.type(screen.getByLabelText(/confirm password/i), 'Str0ng!different')
      await user.click(screen.getByRole('button', { name: /create account/i }))

      expect(await screen.findByText(/passwords do not match/i)).toBeInTheDocument()
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('does not over-enforce complexity on the login form', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn().mockResolvedValue(undefined)
      render(<LoginForm onSubmit={onSubmit} />)

      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), 'plainpass')
      await user.click(screen.getByRole('button', { name: /login/i }))

      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    })
  })
})
