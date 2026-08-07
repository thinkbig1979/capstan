import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HelpHint } from '../help-hint'

describe('HelpHint', () => {
  it('renders a "Learn more" link to the given href when one is provided', async () => {
    const user = userEvent.setup()
    render(
      <HelpHint label="Volumes" title="Volumes" href="https://example.com/docs/volumes.md">
        <p>Where containers store data.</p>
      </HelpHint>,
    )

    await user.click(screen.getByRole('button', { name: 'Help: Volumes' }))

    const link = await screen.findByRole('link', { name: /learn more/i })
    expect(link).toHaveAttribute('href', 'https://example.com/docs/volumes.md')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'))
  })

  it('renders no link when href is omitted', async () => {
    const user = userEvent.setup()
    render(
      <HelpHint label="Volumes" title="Volumes">
        <p>Where containers store data.</p>
      </HelpHint>,
    )

    await user.click(screen.getByRole('button', { name: 'Help: Volumes' }))

    expect(await screen.findByText('Where containers store data.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /learn more/i })).not.toBeInTheDocument()
  })
})
