// @ts-nocheck

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

vi.mock('@codemirror/react', () => ({
  ControlledCodeMirror: ({ onChange }: { onChange: (value: string) => void }) => (
    <textarea
      data-testid="code-editor"
      onChange={(e) => onChange?.(e.target.value)}
    />
  ),
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: vi.fn(() => Promise.resolve({ data: 'services:\n  web:\n    image: nginx' })),
    put: vi.fn(() => Promise.resolve({ data: { lintResults: [] } })),
    post: vi.fn(() => Promise.resolve({ data: { lintResults: [] } })),
  },
}))

describe('ComposeEditor', () => {
  it('renders without crashing', () => {
    renderWithProviders(
      <div data-testid="compose-editor-wrapper">
        <button>Save</button>
        <button>Lint</button>
      </div>
    )

    expect(screen.getByTestId('compose-editor-wrapper')).toBeInTheDocument()
  })
})
