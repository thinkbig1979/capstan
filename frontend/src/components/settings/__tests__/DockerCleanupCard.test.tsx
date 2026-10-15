import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { DockerCleanupCard } from '../DockerCleanupCard'

// The schedule moved to the Docker host page (agent-os-6qfz, D94). Its tests
// moved with the code: the form's to host/cleanup/__tests__/CleanupPolicyForm.test.tsx,
// preview, history and run-now to host/__tests__/AutoCleanupPanel.test.tsx.
// What stays here is the notice the Settings section now renders.

function renderCard() {
  return render(
    <MemoryRouter>
      <DockerCleanupCard />
    </MemoryRouter>,
  )
}

describe('DockerCleanupCard (moved notice)', () => {
  it('says the cleanup moved and links to the panel on the Docker host page', () => {
    renderCard()
    const moved = screen.getByTestId('settings-docker-cleanup-moved')

    expect(moved).toHaveTextContent('Docker cleanup moved to the Docker host page')
    expect(within(moved).getByRole('link', { name: 'Open automatic cleanup' })).toHaveAttribute(
      'href',
      '/host#automatic-cleanup',
    )
  })

  it('renders no policy form and no run control', () => {
    renderCard()

    expect(screen.queryByLabelText('Age floor (hours)')).toBeNull()
    expect(screen.queryByRole('switch')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save cleanup schedule' })).toBeNull()
    expect(screen.queryByRole('button', { name: /Run/ })).toBeNull()
  })
})
