import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AuthPage } from '../AuthPage'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', () => ({
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
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument()
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
