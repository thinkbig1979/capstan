import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ErrorBoundary } from '../ErrorBoundary'

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

function ThrowError({ error }: { error: Error }): React.ReactElement {
  throw error
}

describe('ErrorBoundary', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders children when no error', () => {
    render(
      <ErrorBoundary>
        <div>Content</div>
      </ErrorBoundary>
    )
    expect(screen.getByText('Content')).toBeInTheDocument()
  })

  it('renders default error UI when child throws', () => {
    render(
      <ErrorBoundary>
        <ThrowError error={new Error('Test error')} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Something went wrong')).toBeInTheDocument()
  })

  it('renders custom fallback when provided', () => {
    render(
      <ErrorBoundary fallback={<div>Custom fallback</div>}>
        <ThrowError error={new Error('Test error')} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Custom fallback')).toBeInTheDocument()
  })

  it('calls onError callback when error caught', () => {
    const onError = vi.fn()
    render(
      <ErrorBoundary onError={onError}>
        <ThrowError error={new Error('Test error')} />
      </ErrorBoundary>
    )
    expect(onError).toHaveBeenCalled()
  })

  it('classifies network errors', () => {
    render(
      <ErrorBoundary>
        <ThrowError error={new Error('Network error occurred')} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Check your connection and try again')).toBeInTheDocument()
  })

  it('classifies auth errors', () => {
    render(
      <ErrorBoundary>
        <ThrowError error={new Error('401 Unauthorized')} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Log in again to continue')).toBeInTheDocument()
  })

  it('shows Contact Support button', () => {
    render(
      <ErrorBoundary>
        <ThrowError error={new Error('Something broke')} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Contact Support')).toBeInTheDocument()
  })
})
