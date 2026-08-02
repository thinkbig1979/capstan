import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuthPage } from '../AuthPage'

const mockNavigate = vi.fn()
vi.mock('react-router', () => ({
  useNavigate: () => mockNavigate,
}))

const mockToastSuccess = vi.fn()
const mockToastError = vi.fn()
vi.mock('sonner', () => ({
  toast: { success: (...args: unknown[]) => mockToastSuccess(...args), error: (...args: unknown[]) => mockToastError(...args) },
}))

describe('AuthPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders title and description', () => {
    render(
      <AuthPage
        title="Sign In"
        description="Enter credentials"
        submitFn={vi.fn()}
        successMessage="Welcome"
        errorPrefix="Login"
      />
    )
    expect(screen.getByText('Sign In')).toBeInTheDocument()
    expect(screen.getByText('Enter credentials')).toBeInTheDocument()
  })

  it('renders custom button text', () => {
    render(
      <AuthPage
        title="Setup"
        description="Create account"
        submitFn={vi.fn()}
        successMessage="Done"
        errorPrefix="Setup"
        buttonText="Create Account"
      />
    )
    expect(screen.getByRole('button', { name: /create account/i })).toBeInTheDocument()
  })

  it('renders username and password fields', () => {
    render(
      <AuthPage
        title="Sign In"
        description="Enter credentials"
        submitFn={vi.fn()}
        successMessage="Welcome"
        errorPrefix="Login"
      />
    )
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
  })

  it('shows a failure inline only, without a duplicate error toast (AU-4)', async () => {
    const user = userEvent.setup()
    const submitFn = vi.fn().mockRejectedValue(new Error('Invalid username or password'))
    render(
      <AuthPage
        title="Sign In"
        description="Enter credentials"
        submitFn={submitFn}
        successMessage="Welcome"
        errorPrefix="Login"
      />
    )

    await user.type(screen.getByLabelText(/username/i), 'admin')
    await user.type(screen.getByLabelText('Password'), 'plainpass')
    await user.click(screen.getByRole('button', { name: /login/i }))

    expect(await screen.findByText(/invalid username or password/i)).toBeInTheDocument()
    await waitFor(() => expect(submitFn).toHaveBeenCalledTimes(1))
    expect(mockToastError).not.toHaveBeenCalled()
  })

  it('renders login form within a card', () => {
    render(
      <AuthPage
        title="Sign In"
        description="Enter credentials"
        submitFn={vi.fn()}
        successMessage="Welcome"
        errorPrefix="Login"
      />
    )
    expect(screen.getByText('Sign In')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument()
  })
})
