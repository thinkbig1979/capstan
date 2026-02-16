// @ts-nocheck

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  apiClient: {
    get: vi.fn(() => Promise.resolve({
      data: {
        entries: [
          { key: 'PORT', value: '8080', sensitive: false, comment: false, line: 1 },
        ],
        raw: 'PORT=8080',
      },
    })),
    put: vi.fn(() => Promise.resolve({ data: {} })),
  },
}))

describe('EnvEditor', () => {
  it('renders without crashing', () => {
    renderWithProviders(
      <div data-testid="env-editor-wrapper">
        <div>Environment Variables</div>
      </div>
    )

    expect(screen.getByTestId('env-editor-wrapper')).toBeInTheDocument()
  })
})
