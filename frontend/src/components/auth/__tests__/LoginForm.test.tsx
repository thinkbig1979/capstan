// @ts-nocheck

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LoginForm } from '../LoginForm'

describe('LoginForm', () => {
  it('renders login form with username and password fields', () => {
    const onSubmit = vi.fn()
    render(<LoginForm onSubmit={onSubmit} />)

    expect(screen.getByLabelText(/username/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument()
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
