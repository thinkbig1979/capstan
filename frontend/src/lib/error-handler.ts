export type ErrorType = 'network' | 'auth' | 'validation' | 'server' | 'timeout' | 'unknown'

export interface AppError {
  message: string
  type: ErrorType
  status?: number
  retryable: boolean
  originalError?: unknown
  context?: string
  action?: string
}

export function classifyError(error: unknown): AppError {
  if (!error) {
    return {
      message: 'An unexpected error occurred',
      type: 'unknown',
      retryable: false,
    }
  }

  const err = error as { 
    response?: { status?: number; data?: { error?: string; message?: string; details?: Record<string, unknown> } }; 
    code?: string; 
    message?: string 
  }
  const status = err.response?.status
  const message = err.response?.data?.error || err.response?.data?.message || err.message || 'An error occurred'
  const details = err.response?.data?.details

  if (err.code === 'ECONNABORTED' || err.code === 'ETIMEDOUT' || status === 408) {
    return {
      message: 'The request timed out. Please try again.',
      type: 'timeout',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (err.code === 'ERR_NETWORK' || !navigator.onLine) {
    return {
      message: 'Check your connection and try again',
      type: 'network',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (status === 401) {
    return {
      message: 'Log in again to continue',
      type: 'auth',
      status,
      retryable: false,
      originalError: error,
      action: 'Log In',
    }
  }

  if (status === 403) {
    return {
      message: 'You do not have permission to perform this action',
      type: 'auth',
      status,
      retryable: false,
      originalError: error,
      action: 'Log In',
    }
  }

  if (status === 404) {
    return {
      message: 'The requested resource was not found',
      type: 'server',
      status,
      retryable: false,
      originalError: error,
      context: details?.resource as string,
    }
  }

  if (status === 422 || status === 400) {
    const fieldErrors = details as Record<string, string> | undefined
    const fieldMessage = fieldErrors 
      ? Object.entries(fieldErrors).map(([field, err]) => `${field}: ${err}`).join(', ')
      : message

    return {
      message: fieldMessage || 'Please check your input and try again',
      type: 'validation',
      status,
      retryable: false,
      originalError: error,
      action: 'Fix',
    }
  }

  if (status === 429) {
    return {
      message: 'Too many requests. Please wait a moment and try again',
      type: 'server',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (status && status >= 500) {
    return {
      message: `${status}: Something went wrong on the server`,
      type: 'server',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (message.toLowerCase().includes('network') || message.toLowerCase().includes('fetch failed')) {
    return {
      message: 'Check your connection and try again',
      type: 'network',
      status,
      retryable: true,
      originalError: error,
      action: 'Retry',
    }
  }

  if (message.toLowerCase().includes('validation') || message.toLowerCase().includes('invalid')) {
    return {
      message: message || 'Please check your input and try again',
      type: 'validation',
      status,
      retryable: false,
      originalError: error,
      action: 'Fix',
    }
  }

  return {
    message: message || 'An unexpected error occurred',
    type: 'unknown',
    status,
    retryable: true,
    originalError: error,
    action: 'Contact Support',
  }
}

export function getErrorIcon(type: ErrorType): string {
  const icons = {
    network: '🌐',
    auth: '🔐',
    validation: '⚠️',
    server: '🔧',
    timeout: '⏱️',
    unknown: '❓',
  }
  return icons[type]
}

export function canRetry(error: AppError): boolean {
  return error.retryable
}

export function copyErrorToClipboard(error: AppError): void {
  const errorText = `
Error: ${error.message}
Type: ${error.type}
Status: ${error.status || 'N/A'}
Timestamp: ${new Date().toISOString()}
URL: ${window.location.href}
User Agent: ${navigator.userAgent}
  `.trim()
  
  navigator.clipboard.writeText(errorText).catch(() => {})
}
