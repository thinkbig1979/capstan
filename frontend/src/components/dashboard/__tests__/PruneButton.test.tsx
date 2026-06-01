import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentProps, ReactNode } from 'react'
import { PruneButton, type PruneOptionConfig } from '../PruneButton'

type PruneFnProp = ComponentProps<typeof PruneButton>['pruneFn']

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

function renderButton(pruneFn: ReturnType<typeof vi.fn>, options?: PruneOptionConfig) {
  return render(
    <PruneButton
      resourceType="image"
      pruneFn={pruneFn as unknown as PruneFnProp}
      options={options}
      confirmMessage="Prune Unused Images?"
      confirmDescription="desc"
      invalidateKeys={[['resources', 'images']]}
    />,
    { wrapper },
  )
}

beforeEach(() => vi.clearAllMocks())

describe('PruneButton', () => {
  it('does a basic prune (no flags) when confirmed without changing options', async () => {
    const user = userEvent.setup()
    const pruneFn = vi.fn().mockResolvedValue({ deleted: [], spaceReclaimed: 0 })
    renderButton(pruneFn, { all: { label: 'Remove all unused images, not just dangling' }, until: true })

    await user.click(screen.getByRole('button', { name: /prune/i }))
    // Popover opened with the title and the Confirm button.
    expect(await screen.findByText('Prune Unused Images?')).toBeInTheDocument()
    const confirm = screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
    await user.click(confirm)

    await waitFor(() => expect(pruneFn).toHaveBeenCalledTimes(1))
    expect(pruneFn.mock.calls[0][0]).toEqual({ all: false, until: undefined })
  })

  it('passes the selected all + until flags to pruneFn', async () => {
    const user = userEvent.setup()
    const pruneFn = vi.fn().mockResolvedValue({ deleted: ['a'], spaceReclaimed: 10 })
    renderButton(pruneFn, { all: { label: 'Remove all unused images, not just dangling' }, until: true })

    await user.click(screen.getByRole('button', { name: /prune/i }))
    await screen.findByText('Prune Unused Images?')

    await user.click(screen.getByRole('switch')) // enable "all unused"
    await user.click(screen.getByRole('button', { name: '24h' })) // age preset
    const confirm = screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!
    await user.click(confirm)

    await waitFor(() => expect(pruneFn).toHaveBeenCalledTimes(1))
    expect(pruneFn.mock.calls[0][0]).toEqual({ all: true, until: '24h' })
  })

  it('only shows option controls the resource supports', async () => {
    const user = userEvent.setup()
    const pruneFn = vi.fn().mockResolvedValue({ deleted: [] })
    renderButton(pruneFn, { until: true }) // containers/networks: until only, no "all"

    await user.click(screen.getByRole('button', { name: /prune/i }))
    await screen.findByText('Prune Unused Images?')

    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7d' })).toBeInTheDocument()
  })
})
