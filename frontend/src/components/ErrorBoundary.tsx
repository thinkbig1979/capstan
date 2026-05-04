import React, { type ErrorInfo, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { AlertCircle, RefreshCw, Copy } from 'lucide-react'
import { toast } from 'sonner'

interface ErrorBoundaryProps {
  children: ReactNode
  fallback?: ReactNode
  onError?: (error: Error, errorInfo: ErrorInfo) => void
}

interface ErrorBoundaryState {
  hasError: boolean
  error?: Error
  errorInfo?: ErrorInfo
}

function getErrorType(error?: Error): 'network' | 'auth' | 'validation' | 'server' | 'unknown' {
  if (!error) return 'unknown'

  const message = error.message.toLowerCase()

  if (message.includes('network') || message.includes('fetch')) return 'network'
  if (message.includes('401') || message.includes('unauthorized')) return 'auth'
  if (message.includes('403') || message.includes('forbidden')) return 'auth'
  if (message.includes('validation') || message.includes('invalid')) return 'validation'
  if (message.includes('500') || message.includes('502') || message.includes('503')) return 'server'

  return 'unknown'
}

function getUserFriendlyMessage(error?: Error): string {
  if (!error) return 'An unexpected error occurred'

  const errorType = getErrorType(error)

  const messages: Record<string, string> = {
    network: 'Check your connection and try again',
    auth: 'Log in again to continue',
    validation: 'Please check your input and try again',
    server: 'Something went wrong on our end. Please try again later',
    unknown: 'An unexpected error occurred. Please try refreshing the page',
  }

  return messages[errorType]
}

function getErrorAction(errorType: string): string {
  const actions: Record<string, string> = {
    network: 'Retry',
    auth: 'Log In',
    validation: 'Fix',
    server: 'Retry',
    unknown: 'Contact Support',
  }

  return actions[errorType] || 'Retry'
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text).then(() => {
    toast.success('Error details copied to clipboard')
  }).catch(() => {
    toast.error('Failed to copy error details')
  })
}

export class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props)
    this.state = { hasError: false }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    this.setState({ errorInfo })
    
    const isDev = import.meta.env.DEV
    if (isDev) {
      console.error('ErrorBoundary caught an error:', error, errorInfo)
    }
    this.props.onError?.(error, errorInfo)
  }

  handleRetry = () => {
    window.location.reload()
  }

  handleReportIssue = () => {
    window.location.reload()
  }

  getErrorDetails = (): string => {
    const { error, errorInfo } = this.state
    return `
Error: ${error?.message || 'Unknown error'}
Stack: ${error?.stack || 'No stack trace'}
Component Stack: ${errorInfo?.componentStack || 'No component stack'}
URL: ${window.location.href}
User Agent: ${navigator.userAgent}
Timestamp: ${new Date().toISOString()}
    `.trim()
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback
      }

      const errorType = getErrorType(this.state.error)
      const userMessage = getUserFriendlyMessage(this.state.error)
      const errorAction = getErrorAction(errorType)

      return (
        <div className="flex min-h-[400px] items-center justify-center p-4">
          <Card className="max-w-2xl w-full">
            <CardHeader>
              <div className="flex items-center justify-center mb-4">
                <AlertCircle className="h-12 w-12 text-destructive" />
              </div>
              <CardTitle className="text-center">Something went wrong</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <p className="text-center text-muted-foreground">{userMessage}</p>

              <div className="flex flex-wrap gap-2 justify-center">
                {errorAction !== 'Log In' && (
                  <Button onClick={this.handleRetry} className="min-h-[44px]">
                    <RefreshCw className="mr-2 h-4 w-4" />
                    {errorAction}
                  </Button>
                )}
                <Button
                  variant="outline"
                  onClick={this.handleReportIssue}
                  className="min-h-[44px]"
                >
                  <RefreshCw className="mr-2 h-4 w-4" />
                  Refresh Page
                </Button>
              </div>

              {import.meta.env.DEV && this.state.error && (
                <details className="mt-4">
                  <summary className="cursor-pointer text-sm font-medium text-muted-foreground hover:text-foreground">
                    Show Error Details
                  </summary>
                  <div className="mt-2 p-4 bg-muted rounded-md">
                    <div className="space-y-2">
                      <div>
                        <p className="text-sm font-medium mb-1">Error Type:</p>
                        <code className="text-xs bg-background px-2 py-1 rounded">{errorType}</code>
                      </div>
                      <div>
                        <p className="text-sm font-medium mb-1">Error Message:</p>
                        <pre className="text-xs bg-background p-2 rounded overflow-x-auto">
                          {this.state.error.message}
                        </pre>
                      </div>
                      {this.state.error.stack && (
                        <div>
                          <p className="text-sm font-medium mb-1">Stack Trace:</p>
                          <pre className="text-xs bg-background p-2 rounded overflow-x-auto max-h-48">
                            {this.state.error.stack}
                          </pre>
                        </div>
                      )}
                      {this.state.errorInfo?.componentStack && (
                        <div>
                          <p className="text-sm font-medium mb-1">Component Stack:</p>
                          <pre className="text-xs bg-background p-2 rounded overflow-x-auto max-h-48">
                            {this.state.errorInfo.componentStack}
                          </pre>
                        </div>
                      )}
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => copyToClipboard(this.getErrorDetails())}
                        className="mt-2"
                      >
                        <Copy className="mr-2 h-4 w-4" />
                        Copy Error Details
                      </Button>
                    </div>
                  </div>
                </details>
              )}
            </CardContent>
          </Card>
        </div>
      )
    }

    return this.props.children
  }
}
