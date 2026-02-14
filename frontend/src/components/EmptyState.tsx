import type { ReactNode } from 'react'
import { useEffect } from 'react'

interface EmptyStateProps {
  icon?: ReactNode
  title: string
  description?: string
  action?: ReactNode
}

function sanitizeText(text: string, maxLength: number): string {
  if (!text) return ''
  
  let sanitized = text.replace(/<[^>]*>/g, '')
  
  sanitized = sanitized.replace(/&/g, '&amp;')
  sanitized = sanitized.replace(/</g, '&lt;')
  sanitized = sanitized.replace(/>/g, '&gt;')
  sanitized = sanitized.replace(/"/g, '&quot;')
  sanitized = sanitized.replace(/'/g, '&#x27;')
  
  if (sanitized.length > maxLength) {
    sanitized = sanitized.substring(0, maxLength) + '...'
  }
  
  return sanitized
}

function validateNoHTML(text: string): boolean {
  return !/<[^>]*>/.test(text)
}

const DefaultIcon = () => (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="48"
    height="48"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    className="text-muted-foreground"
  >
    <circle cx="12" cy="12" r="10" />
    <path d="M12 8v8" />
    <path d="M8 12h8" />
  </svg>
)

export function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  useEffect(() => {
    if (!validateNoHTML(title) || (description && !validateNoHTML(description))) {
      console.warn('EmptyState: HTML detected in props, which may indicate an XSS attempt')
    }
  }, [title, description])

  const sanitizedTitle = sanitizeText(title, 100)
  const sanitizedDescription = description ? sanitizeText(description, 500) : undefined

  return (
    <div className="flex flex-col items-center justify-center min-h-[400px] text-center p-8">
      {icon || <DefaultIcon />}
      <h3 className="text-lg font-semibold mt-4 mb-2">{sanitizedTitle}</h3>
      {sanitizedDescription && <p className="text-sm text-muted-foreground mb-4 max-w-md">{sanitizedDescription}</p>}
      {action && <div>{action}</div>}
    </div>
  )
}

export function NoDirectories({ onScan }: { onScan: () => void }) {
  return (
    <EmptyState
      title="No directories found"
      description="Scan your stacks directory to discover Docker Compose stacks."
      action={
        <button
          onClick={onScan}
          className="inline-flex items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 min-h-[44px]"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="mr-2"
          >
            <path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11" />
          </svg>
          Scan Directories
        </button>
      }
    />
  )
}

export function NoStacks() {
  return (
    <EmptyState
      title="No stacks found"
      description="No Docker Compose stacks were found in this directory."
    />
  )
}

export function NoContainers() {
  return (
    <EmptyState
      title="No containers"
      description="This stack has no running containers."
    />
  )
}

export function NoGitHistory() {
  return (
    <EmptyState
      title="No git history"
      description="This directory is not a git repository or has no commits."
    />
  )
}

export function NoLogs() {
  return (
    <EmptyState
      title="No logs"
      description="No logs available for this container."
    />
  )
}

export function NoEnvVars() {
  return (
    <EmptyState
      title="No environment variables"
      description="No environment variables defined for this stack."
    />
  )
}
