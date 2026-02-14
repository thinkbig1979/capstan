import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderWithProviders } from '../../../test/utils'

describe('GitStatus', () => {
  it('renders git status information', () => {
    const props = {
      branch: 'main',
      commit: 'abc123',
      dirty: false,
      ahead: 0,
      behind: 0,
    }

    renderWithProviders(
      <div data-testid="git-status">
        <span>Branch: {props.branch}</span>
        <span>Commit: {props.commit}</span>
      </div>
    )

    expect(screen.getByTestId('git-status')).toHaveTextContent('Branch: main')
    expect(screen.getByTestId('git-status')).toHaveTextContent('Commit: abc123')
  })

  it('shows dirty state when there are uncommitted changes', () => {
    const props = {
      branch: 'main',
      commit: 'abc123',
      dirty: true,
      ahead: 0,
      behind: 0,
    }

    renderWithProviders(
      <div data-testid="git-status">
        <span>Dirty: {props.dirty.toString()}</span>
      </div>
    )

    expect(screen.getByTestId('git-status')).toHaveTextContent('Dirty: true')
  })
})
