import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { OperationProgress } from '../OperationProgress'

const noop = vi.fn()

// ─── idle ─────────────────────────────────────────────────────────────────────

describe('OperationProgress — idle', () => {
  it('renders nothing when status is idle', () => {
    const { container } = render(
      <OperationProgress status="idle" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    expect(container.firstChild).toBeNull()
  })
})

// ─── running ──────────────────────────────────────────────────────────────────

describe('OperationProgress — running', () => {
  it('shows the action label and line count', () => {
    render(
      <OperationProgress
        status="running"
        lines={['line 1', 'line 2']}
        action="start"
        error={null}
        onDismiss={noop}
      />,
    )
    expect(screen.getByText('Starting stack')).toBeDefined()
    expect(screen.getByText('(2 lines)')).toBeDefined()
  })

  it('does not show dismiss button while running', () => {
    render(
      <OperationProgress status="running" lines={[]} action="pull" error={null} onDismiss={noop} />,
    )
    expect(screen.queryByRole('button')).toBeNull()
  })
})

// ─── success ──────────────────────────────────────────────────────────────────

describe('OperationProgress — success', () => {
  it('renders "Operation completed" as the header label', () => {
    render(
      <OperationProgress status="success" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    expect(screen.getByText('Operation completed')).toBeDefined()
  })

  it('shows a dismiss button', () => {
    render(
      <OperationProgress status="success" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    expect(screen.getByRole('button')).toBeDefined()
  })

  it('applies success color classes (bg-success/10)', () => {
    const { container } = render(
      <OperationProgress status="success" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    const header = container.querySelector('[class*="bg-success"]')
    expect(header).not.toBeNull()
  })
})

// ─── no_change — must NOT look like success ───────────────────────────────────

describe('OperationProgress — no_change', () => {
  it('renders the no_change label, NOT "Operation completed"', () => {
    render(
      <OperationProgress status="no_change" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    expect(screen.getByText('No change — already in desired state')).toBeDefined()
    expect(screen.queryByText('Operation completed')).toBeNull()
  })

  it('does NOT apply success color classes', () => {
    const { container } = render(
      <OperationProgress status="no_change" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    const successEl = container.querySelector('[class*="bg-success"]')
    expect(successEl).toBeNull()
  })

  it('applies info color classes (bg-info/10)', () => {
    const { container } = render(
      <OperationProgress status="no_change" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    const infoEl = container.querySelector('[class*="bg-info"]')
    expect(infoEl).not.toBeNull()
  })

  it('shows a dismiss button', () => {
    render(
      <OperationProgress status="no_change" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    expect(screen.getByRole('button')).toBeDefined()
  })
})

// ─── partial ─────────────────────────────────────────────────────────────────

describe('OperationProgress — partial', () => {
  it('renders "Operation partially completed" label', () => {
    render(
      <OperationProgress status="partial" lines={[]} action="restart" error={null} onDismiss={noop} />,
    )
    expect(screen.getByText('Operation partially completed')).toBeDefined()
  })

  it('applies warning color classes (bg-warning/10)', () => {
    const { container } = render(
      <OperationProgress status="partial" lines={[]} action="restart" error={null} onDismiss={noop} />,
    )
    const warningEl = container.querySelector('[class*="bg-warning"]')
    expect(warningEl).not.toBeNull()
  })

  it('does NOT apply success color classes', () => {
    const { container } = render(
      <OperationProgress status="partial" lines={[]} action="restart" error={null} onDismiss={noop} />,
    )
    expect(container.querySelector('[class*="bg-success"]')).toBeNull()
  })
})

// ─── error ────────────────────────────────────────────────────────────────────

describe('OperationProgress — error', () => {
  it('renders "Operation failed" label', () => {
    render(
      <OperationProgress status="error" lines={[]} action="start" error="Command failed" onDismiss={noop} />,
    )
    expect(screen.getByText('Operation failed')).toBeDefined()
  })

  it('renders the error message in the footer', () => {
    render(
      <OperationProgress status="error" lines={[]} action="start" error="Out of memory" onDismiss={noop} />,
    )
    expect(screen.getByText('Out of memory')).toBeDefined()
  })

  it('applies destructive color classes', () => {
    const { container } = render(
      <OperationProgress status="error" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    const errEl = container.querySelector('[class*="bg-destructive"]')
    expect(errEl).not.toBeNull()
  })

  it('does NOT apply success color classes', () => {
    const { container } = render(
      <OperationProgress status="error" lines={[]} action="start" error={null} onDismiss={noop} />,
    )
    expect(container.querySelector('[class*="bg-success"]')).toBeNull()
  })
})

// ─── lines rendering ─────────────────────────────────────────────────────────

describe('OperationProgress — lines rendering', () => {
  it('renders all lines', () => {
    render(
      <OperationProgress
        status="success"
        lines={['Step 1 done', 'Step 2 done']}
        action="pull"
        error={null}
        onDismiss={noop}
      />,
    )
    expect(screen.getByText('Step 1 done')).toBeDefined()
    expect(screen.getByText('Step 2 done')).toBeDefined()
  })
})
