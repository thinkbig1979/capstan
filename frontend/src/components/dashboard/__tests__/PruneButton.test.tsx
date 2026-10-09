import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentProps, ReactNode } from 'react'
import { toast } from 'sonner'
import { PruneButton, type PruneOptionConfig } from '../PruneButton'

type PruneFnProp = ComponentProps<typeof PruneButton>['pruneFn']

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
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
    const pruneFn = vi.fn().mockResolvedValue({ outcome: 'no_change', reason: 'nothing to prune', details: { deleted: [], spaceReclaimed: 0 } })
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
    const pruneFn = vi.fn().mockResolvedValue({ outcome: 'success', reason: 'pruned', details: { deleted: ['a'], spaceReclaimed: 10 } })
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
    const pruneFn = vi.fn().mockResolvedValue({ outcome: 'no_change', reason: 'nothing to prune', details: { deleted: [] } })
    renderButton(pruneFn, { until: true }) // containers/networks: until only, no "all"

    await user.click(screen.getByRole('button', { name: /prune/i }))
    await screen.findByText('Prune Unused Images?')

    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7d' })).toBeInTheDocument()
  })

  describe('done-state indicator', () => {
    async function pruneWith(result: unknown) {
      const user = userEvent.setup()
      const pruneFn = vi.fn().mockResolvedValue(result)
      const view = renderButton(pruneFn)
      await user.click(screen.getByRole('button', { name: /prune/i }))
      await screen.findByText('Prune Unused Images?')
      await user.click(screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!)
      await waitFor(() => expect(pruneFn).toHaveBeenCalledTimes(1))
      return view
    }

    it('shows a warning indicator, not the green check, for a partial prune', async () => {
      const { container } = await pruneWith({
        outcome: 'partial',
        reason: 'removed 2, 1 failed',
        details: { deleted: ['a', 'b'], spaceReclaimed: 10 },
      })

      expect(await screen.findByText(/partly pruned 2 images/i)).toBeInTheDocument()
      expect(container.querySelector('svg.text-warning')).not.toBeNull()
      expect(container.querySelector('.text-success')).toBeNull()
      expect(screen.queryByText(/^pruned/i)).not.toBeInTheDocument()
    })

    it('keeps the green check for a full success', async () => {
      const { container } = await pruneWith({
        outcome: 'success',
        reason: 'pruned',
        details: { deleted: ['a', 'b'], spaceReclaimed: 10 },
      })

      expect(await screen.findByText(/pruned 2 images/i)).toBeInTheDocument()
      expect(container.querySelector('svg.text-success')).not.toBeNull()
      expect(container.querySelector('.text-warning')).toBeNull()
    })

    it('keeps the muted indicator for no_change', async () => {
      const { container } = await pruneWith({
        outcome: 'no_change',
        reason: 'nothing to prune',
        details: { deleted: [] },
      })

      expect(await screen.findByText('nothing to prune')).toBeInTheDocument()
      expect(container.querySelector('.text-success')).toBeNull()
      expect(container.querySelector('.text-warning')).toBeNull()
    })
  })
})

// ─── Toasts, invalidation and errors on useActionMutation (agent-os-cdsh) ─────

describe('PruneButton — outcome toasts, invalidation and rejections', () => {
  async function prune(pruneFn: ReturnType<typeof vi.fn>, onPruneComplete?: () => void) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={queryClient}>
        <PruneButton
          resourceType="image"
          pruneFn={pruneFn as unknown as PruneFnProp}
          confirmMessage="Prune Unused Images?"
          confirmDescription="desc"
          invalidateKeys={[['resources', 'images'], ['dashboard-stats']]}
          onPruneComplete={onPruneComplete}
        />
      </QueryClientProvider>,
    )
    await user.click(screen.getByRole('button', { name: /prune/i }))
    await screen.findByText('Prune Unused Images?')
    await user.click(screen.getAllByRole('button', { name: /^prune$/i }).at(-1)!)
    await waitFor(() => expect(pruneFn).toHaveBeenCalledTimes(1))
    return { invalidate }
  }

  it('titles a success toast with the count, tags and space reclaimed, then invalidates and calls onPruneComplete', async () => {
    const onPruneComplete = vi.fn()
    const { invalidate } = await prune(
      vi.fn().mockResolvedValue({
        outcome: 'success',
        reason: 'pruned',
        details: { imagesDeleted: 4, tagsRemoved: 2, spaceReclaimed: 2 * 1024 * 1024 * 1024 },
      }),
      onPruneComplete,
    )

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Pruned 4 images, 2 tags, 2.00 GB reclaimed'))
    expect(toast.info).not.toHaveBeenCalled()
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['resources', 'images'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['dashboard-stats'] })
    expect(onPruneComplete).toHaveBeenCalledTimes(1)
  })

  it('shows no_change as info with the backend reason, never as a success', async () => {
    await prune(vi.fn().mockResolvedValue({ outcome: 'no_change', reason: 'nothing to prune', details: { deleted: [] } }))

    await waitFor(() => expect(toast.info).toHaveBeenCalledWith('nothing to prune'))
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('shows a partial prune as a warning carrying the backend reason', async () => {
    await prune(vi.fn().mockResolvedValue({ outcome: 'partial', reason: 'removed 2, 1 failed', details: { deleted: ['a', 'b'] } }))

    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('removed 2, 1 failed'))
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('titles a rejected failed result with the action and gives the reason as the description', async () => {
    await prune(vi.fn().mockRejectedValue({ outcome: 'failed', reason: 'Docker daemon unreachable', status: 503 }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to prune image', { description: 'Docker daemon unreachable' }),
    )
    expect(await screen.findByText('Prune failed')).toBeInTheDocument()
  })

  it('keeps a rejected partial at warning level', async () => {
    await prune(vi.fn().mockRejectedValue({ outcome: 'partial', reason: 'removed 1, 2 failed' }))

    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('removed 1, 2 failed'))
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('titles a rejection that is not an ActionResult with the action', async () => {
    await prune(vi.fn().mockRejectedValue({ code: 'ERR_NETWORK' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to prune image', { description: 'Check your connection and try again' }),
    )
  })

  it('never toasts an empty error for a 2xx failed result with no reason', async () => {
    await prune(vi.fn().mockResolvedValue({ outcome: 'failed', reason: '' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to prune image'))
  })
})
