import React, { type ErrorInfo, type ReactNode } from 'react'

interface ErrorBoundaryProps {
  children: ReactNode
  fallback?: ReactNode
  onError?: (error: Error, errorInfo: ErrorInfo) => void
}

interface ErrorBoundaryState {
  hasError: boolean
  error?: Error
}

function sanitizeErrorMessage(message: string): string {
  if (!message) return 'An unexpected error occurred'
  
  let sanitized = message.replace(/<[^>]*>/g, '')
  
  sanitized = sanitized.replace(/&/g, '&amp;')
  sanitized = sanitized.replace(/</g, '&lt;')
  sanitized = sanitized.replace(/>/g, '&gt;')
  sanitized = sanitized.replace(/"/g, '&quot;')
  sanitized = sanitized.replace(/'/g, '&#x27;')
  
  if (sanitized.length > 200) {
    sanitized = sanitized.substring(0, 200) + '...'
  }
  
  return sanitized
}

function getSafeErrorMessage(error?: Error): string {
  if (!error) return 'An unexpected error occurred'
  
  const message = error.message.toLowerCase()
  
  const safeMessages: Record<string, string> = {
    'network': 'A network error occurred. Please check your connection.',
    'timeout': 'The request timed out. Please try again.',
    'unauthorized': 'You are not authorized to perform this action.',
    'forbidden': 'Access denied.',
    'not found': 'The requested resource was not found.',
    'parse': 'There was a problem processing the data.',
    'validation': 'Please check your input and try again.',
  }
  
  for (const [key, safeMsg] of Object.entries(safeMessages)) {
    if (message.includes(key)) {
      return safeMsg
    }
  }
  
  return sanitizeErrorMessage(error.message)
}

const ErrorIcon = () => (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="48"
    height="48"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    className="mx-auto mb-4 text-destructive"
  >
    <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" />
    <path d="M12 9v4" />
    <path d="M12 17h.01" />
  </svg>
)

export class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props)
    this.state = { hasError: false }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    const isDev = import.meta.env.DEV
    if (isDev) {
      console.error('ErrorBoundary caught an error:', error, errorInfo)
    }
    this.props.onError?.(error, errorInfo)
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback
      }

      return (
        <div className="flex min-h-[400px] items-center justify-center">
          <div className="text-center">
            <ErrorIcon />
            <h3 className="text-lg font-semibold mb-2">Something went wrong</h3>
            <p className="text-sm text-muted-foreground mb-4">
              {getSafeErrorMessage(this.state.error)}
            </p>
            <button
              onClick={() => window.location.reload()}
              className="inline-flex items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 min-h-[44px]"
            >
              Retry
            </button>
          </div>
        </div>
      )
    }

    return this.props.children
  }
}
