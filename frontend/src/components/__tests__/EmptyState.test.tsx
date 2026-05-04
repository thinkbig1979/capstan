import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EmptyState, NoDirectories, NoStacks, NoContainers, NoGitHistory, NoLogs, NoEnvVars } from '../EmptyState'

describe('EmptyState', () => {
  it('renders title', () => {
    render(<EmptyState title="Nothing here" />)
    expect(screen.getByText('Nothing here')).toBeInTheDocument()
  })

  it('renders description when provided', () => {
    render(<EmptyState title="Empty" description="No items found" />)
    expect(screen.getByText('No items found')).toBeInTheDocument()
  })

  it('does not render description when omitted', () => {
    const { container } = render(<EmptyState title="Empty" />)
    expect(container.querySelector('.text-muted-foreground.mb-4')).not.toBeInTheDocument()
  })

  it('renders custom icon when provided', () => {
    render(<EmptyState title="Empty" icon={<span data-testid="custom-icon">X</span>} />)
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument()
  })

  it('renders default icon when no icon provided', () => {
    render(<EmptyState title="Empty" />)
    expect(document.querySelector('svg')).toBeInTheDocument()
  })

  it('renders action when provided', () => {
    render(<EmptyState title="Empty" action={<button>Action</button>} />)
    expect(screen.getByRole('button', { name: 'Action' })).toBeInTheDocument()
  })
})

describe('NoDirectories', () => {
  it('renders scan button that calls onScan', () => {
    const onScan = vi.fn()
    render(<NoDirectories onScan={onScan} />)
    expect(screen.getByText('Scan Directories')).toBeInTheDocument()
  })
})

describe('NoStacks', () => {
  it('renders no stacks message', () => {
    render(<NoStacks />)
    expect(screen.getByText('No stacks found')).toBeInTheDocument()
  })
})

describe('NoContainers', () => {
  it('renders no containers message', () => {
    render(<NoContainers />)
    expect(screen.getByText('No containers')).toBeInTheDocument()
  })
})

describe('NoGitHistory', () => {
  it('renders no git history message', () => {
    render(<NoGitHistory />)
    expect(screen.getByText('No git history')).toBeInTheDocument()
  })
})

describe('NoLogs', () => {
  it('renders no logs message', () => {
    render(<NoLogs />)
    expect(screen.getByText('No logs')).toBeInTheDocument()
  })
})

describe('NoEnvVars', () => {
  it('renders no env vars message', () => {
    render(<NoEnvVars />)
    expect(screen.getByText('No environment variables')).toBeInTheDocument()
  })
})
