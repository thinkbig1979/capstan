import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { LoginForm, type LoginFormData } from '../LoginForm'

describe('LoginForm', () => {
  it('renders login form with username and password fields', () => {
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} />)

    expect(screen.getByLabelText(/username/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument()
  })

  it('displays validation errors for short username', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} />)

    const usernameInput = screen.getByLabelText(/username/i)
    await user.type(usernameInput, 'ab')
    await user.tab()

    await waitFor(() => {
      expect(screen.getByText(/username must be at least 3 characters/i)).toBeInTheDocument()
    })

    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('displays validation errors for short password', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} />)

    const passwordInput = screen.getByLabelText(/password/i)
    await user.type(passwordInput, 'short')
    await user.tab()

    await waitFor(() => {
      expect(screen.getByText(/password must be at least 8 characters/i)).toBeInTheDocument()
    })

    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('submits form with valid data', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<LoginForm onSubmit={onSubmit} />)

    await user.type(screen.getByLabelText(/username/i), 'testuser')
    await user.type(screen.getByLabelText(/password/i), 'testpassword123')
    await user.click(screen.getByRole('button', { name: /login/i }))

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          username: 'testuser',
          password: 'testpassword123',
        })
      )
    })
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
})
