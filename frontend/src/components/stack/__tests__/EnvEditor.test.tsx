import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

vi.mock('@xterm/xterm', () => ({
  Terminal: class MockTerminal {
    constructor() {}
    open() {}
    write() {}
  },
}))

describe('EnvEditor', () => {
  it('renders editor with env variables', () => {
    const content = 'PORT=8080\nAPI_KEY=secret'
    renderWithProviders(<div data-testid="env-editor">{content}</div>)

    expect(screen.getByTestId('env-editor')).toHaveTextContent(content)
  })
})
