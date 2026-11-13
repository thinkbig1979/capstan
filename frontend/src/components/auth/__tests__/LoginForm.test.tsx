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

    // bcrypt refuses passwords over 72 bytes, and the server's policy now says
    // so (agent-os-orxo). The form mirrors it in bytes, not characters.
    const TOO_LONG = 'Password must not exceed 72 bytes (accented and non-Latin characters count as 2-4 bytes each)'

    it('blocks a password over 72 bytes when enforceComplexity is set', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn()
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      const password = 'Aa1!' + 'x'.repeat(69)
      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), password)
      await user.type(screen.getByLabelText(/confirm password/i), password)
      await user.click(screen.getByRole('button', { name: /create account/i }))

      expect(await screen.findByText(TOO_LONG)).toBeInTheDocument()
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('counts the 72-byte limit in bytes, not characters', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn()
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      // 29 characters, 79 bytes in UTF-8.
      const password = 'Aa1!' + '€'.repeat(25)
      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), password)
      await user.type(screen.getByLabelText(/confirm password/i), password)
      await user.click(screen.getByRole('button', { name: /create account/i }))

      expect(await screen.findByText(TOO_LONG)).toBeInTheDocument()
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('accepts a password of exactly 72 bytes', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn().mockResolvedValue(undefined)
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      const password = 'Aa1!' + 'x'.repeat(68)
      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), password)
      await user.type(screen.getByLabelText(/confirm password/i), password)
      await user.click(screen.getByRole('button', { name: /create account/i }))

      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
      expect(onSubmit.mock.calls[0][0]).toMatchObject({ username: 'admin', password })
    })

    // react-hook-form moved focus to the first invalid field on a failed
    // submit; the plain-state rewrite has to keep that cue (agent-os-orxo).
    it('focuses the first invalid field after a failed submit', async () => {
      const user = userEvent.setup()
      const onSubmit = vi.fn()
      render(<LoginForm onSubmit={onSubmit} buttonText="Create Account" enforceComplexity />)

      await user.click(screen.getByRole('button', { name: /create account/i }))
      expect(screen.getByLabelText(/username/i)).toHaveFocus()

      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.click(screen.getByRole('button', { name: /create account/i }))
      expect(screen.getByLabelText('Password')).toHaveFocus()

      await user.type(screen.getByLabelText('Password'), 'Str0ng!pass')
      await user.click(screen.getByRole('button', { name: /create account/i }))
      expect(screen.getByLabelText(/confirm password/i)).toHaveFocus()
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('marks an invalid field and links it to its message', async () => {
      const user = userEvent.setup()
      render(<LoginForm onSubmit={vi.fn()} buttonText="Create Account" enforceComplexity />)

      await user.type(screen.getByLabelText(/username/i), 'admin')
      await user.type(screen.getByLabelText('Password'), 'lowercaseonly')
      await user.click(screen.getByRole('button', { name: /create account/i }))

      const password = screen.getByLabelText('Password')
      expect(password).toHaveAttribute('aria-invalid', 'true')
      expect(password).toHaveAccessibleDescription(/uppercase letter/i)
      expect(screen.getByLabelText(/username/i)).not.toHaveAttribute('aria-invalid', 'true')
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
