import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../../test/utils'
import { StackUpdateBadge } from '../StackUpdateBadge'
import type { UpdateJobStatus } from '@/stores/updateJobStore'

describe('StackUpdateBadge', () => {
  describe('renders nothing when count is 0 and no active job', () => {
    it('renders nothing when count is 0 and no jobStatus', () => {
      const { container } = renderWithProviders(
        <StackUpdateBadge count={0} onUpdate={vi.fn()} />,
      )
      expect(container.firstChild).toBeNull()
    })

    it('renders nothing when count is 0 and job is in terminal success state', () => {
      const { container } = renderWithProviders(
        <StackUpdateBadge count={0} onUpdate={vi.fn()} jobStatus="success" />,
      )
      expect(container.firstChild).toBeNull()
    })

    it('renders nothing when count is 0 and job is in terminal error state', () => {
      const { container } = renderWithProviders(
        <StackUpdateBadge count={0} onUpdate={vi.fn()} jobStatus="error" />,
      )
      expect(container.firstChild).toBeNull()
    })
  })

  describe('badge count text', () => {
    it('shows singular "update available" when count is 1', () => {
      renderWithProviders(<StackUpdateBadge count={1} onUpdate={vi.fn()} />)
      expect(screen.getByText('1 update available')).toBeInTheDocument()
    })

    it('shows plural "updates available" when count is 2', () => {
      renderWithProviders(<StackUpdateBadge count={2} onUpdate={vi.fn()} />)
      expect(screen.getByText('2 updates available')).toBeInTheDocument()
    })

    it('shows plural "updates available" when count is 5', () => {
      renderWithProviders(<StackUpdateBadge count={5} onUpdate={vi.fn()} />)
      expect(screen.getByText('5 updates available')).toBeInTheDocument()
    })
  })

  describe('update button', () => {
    it('renders "Update & restart" button when count > 0', () => {
      renderWithProviders(<StackUpdateBadge count={3} onUpdate={vi.fn()} />)
      expect(screen.getByRole('button', { name: /update.*restart/i })).toBeInTheDocument()
    })

    it('calls onUpdate when button is clicked', async () => {
      const user = userEvent.setup()
      const onUpdate = vi.fn()
      renderWithProviders(<StackUpdateBadge count={1} onUpdate={onUpdate} />)
      await user.click(screen.getByRole('button', { name: /update.*restart/i }))
      expect(onUpdate).toHaveBeenCalledTimes(1)
    })

    it('disables the button when updatePending is true', () => {
      renderWithProviders(
        <StackUpdateBadge count={1} onUpdate={vi.fn()} updatePending />,
      )
      expect(screen.getByRole('button', { name: /update.*restart/i })).toBeDisabled()
    })
  })

  describe('active job states', () => {
    it.each<[UpdateJobStatus, string]>([
      ['queued', 'Queued…'],
      ['pulling', 'Pulling…'],
      ['recreating', 'Recreating…'],
    ])('shows phase label "%s" and disables button for status "%s"', async (status, label) => {
      renderWithProviders(
        <StackUpdateBadge count={2} onUpdate={vi.fn()} jobStatus={status} />,
      )
      expect(screen.getByText(label)).toBeInTheDocument()
      expect(screen.getByRole('button')).toBeDisabled()
    })

    it('renders the button even when count is 0 but a job is active (queued)', () => {
      renderWithProviders(
        <StackUpdateBadge count={0} onUpdate={vi.fn()} jobStatus="queued" />,
      )
      // Should not be null — the active job keeps it visible
      expect(screen.getByRole('button')).toBeInTheDocument()
    })
  })
})
