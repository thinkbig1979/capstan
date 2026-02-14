import { describe, it, expect } from 'vitest'
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

describe('ComposeEditor', () => {
  it('renders editor with content', () => {
    const content = 'services:\n  web:\n    image: nginx'
    renderWithProviders(<div data-testid="editor">{content}</div>)

    expect(screen.getByTestId('editor')).toHaveTextContent(content)
  })
})
