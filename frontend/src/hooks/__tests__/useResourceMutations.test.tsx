/**
 * Tests for B3 resource mutation hooks: useDeleteImage, useDeleteVolume, etc.
 *
 * Covers audit findings:
 *  #12 — image delete: no_change/partial (untagged-only) must NOT show green "removed"
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

// ─── Mocks ────────────────────────────────────────────────────────────────────

const mockDeleteImage = vi.fn()
const mockDeleteVolume = vi.fn()
const mockDeleteNetwork = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    deleteImage: (...args: unknown[]) => mockDeleteImage(...args),
    deleteVolume: (...args: unknown[]) => mockDeleteVolume(...args),
    deleteNetwork: (...args: unknown[]) => mockDeleteNetwork(...args),
    createNetwork: vi.fn(),
    images: vi.fn(),
    volumes: vi.fn(),
    networks: vi.fn(),
    buildCache: vi.fn(),
    checkUpdates: vi.fn(),
    updateContainer: vi.fn(),
    updateStack: vi.fn(),
    getUpdateJobs: vi.fn(),
    getUpdateHistory: vi.fn(),
  },
  settingsApi: {},
  autoUpdateApi: {},
}))

const mockToastSuccess = vi.fn()
const mockToastError = vi.fn()
const mockToastWarning = vi.fn()
const mockToastInfo = vi.fn()

vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    warning: (...args: unknown[]) => mockToastWarning(...args),
    info: (...args: unknown[]) => mockToastInfo(...args),
    loading: vi.fn(),
    dismiss: vi.fn(),
  },
}))

import {
  useDeleteImage,
  useDeleteVolume,
  useDeleteNetwork,
} from '../useResources'

// ─── Helpers ──────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─── Image delete: outcome discrimination (#12) ───────────────────────────────
//
// Finding #12: image delete with outcome:'no_change' or 'partial' means the
// image was only untagged (still referenced). The UI must NOT show green
// "Image removed" — it should show info or warning.

describe('useDeleteImage — outcome discrimination (finding #12)', () => {
  it('shows toast.success on outcome:success (image fully deleted)', async () => {
    mockDeleteImage.mockResolvedValue({ outcome: 'success', reason: 'Image deleted' })
    const qc = makeClient()
    const { result } = renderHook(() => useDeleteImage(), { wrapper: wrapper(qc) })

    await act(async () => { result.current.mutate({ id: 'sha256:abc', force: false }) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockToastSuccess).toHaveBeenCalledTimes(1)
    expect(mockToastInfo).not.toHaveBeenCalled()
    expect(mockToastWarning).not.toHaveBeenCalled()
  })

  it('shows toast.info on outcome:no_change (untagged-only, image still referenced)', async () => {
    // no_change = tag removed but image digest still in use → must NOT be green success
    mockDeleteImage.mockResolvedValue({
      outcome: 'no_change',
      reason: 'Image untagged (still referenced by containers)',
    })
    const qc = makeClient()
    const { result } = renderHook(() => useDeleteImage(), { wrapper: wrapper(qc) })

    await act(async () => { result.current.mutate({ id: 'sha256:abc', force: false }) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockToastInfo).toHaveBeenCalledTimes(1)
    expect(mockToastSuccess).not.toHaveBeenCalled()
    expect(mockToastWarning).not.toHaveBeenCalled()
  })

  it('shows toast.warning on outcome:partial (partial deletion)', async () => {
    mockDeleteImage.mockResolvedValue({
      outcome: 'partial',
      reason: 'Some tags removed; image still referenced',
      details: { untagged: ['nginx:latest'], deleted: [] },
    })
    const qc = makeClient()
    const { result } = renderHook(() => useDeleteImage(), { wrapper: wrapper(qc) })

    await act(async () => { result.current.mutate({ id: 'sha256:abc', force: false }) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockToastWarning).toHaveBeenCalledTimes(1)
    expect(mockToastSuccess).not.toHaveBeenCalled()
    expect(mockToastInfo).not.toHaveBeenCalled()
  })

  it('invalidates resources/images and dashboard-stats on any outcome', async () => {
    mockDeleteImage.mockResolvedValue({ outcome: 'success', reason: 'done' })
    const qc = makeClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    const { result } = renderHook(() => useDeleteImage(), { wrapper: wrapper(qc) })

    await act(async () => { result.current.mutate({ id: 'sha256:abc', force: false }) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(spy).toHaveBeenCalledWith({ queryKey: ['resources', 'images'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['dashboard-stats'] })
  })
})

// ─── Delete hooks through useActionMutation (agent-os-cdsh) ──────────────────
//
// The three delete hooks moved from raw useMutation onto useActionMutation.
// Each row pins what the move had to keep (success text, invalidated keys) and
// what it changed on purpose: a rejected `failed` result now carries the
// action as the toast title and the backend reason as its description.

// Each row fires its own hook with its own input, so the table needs no cast.
interface DeleteProbe { fire: () => void; isSuccess: boolean; isError: boolean }
interface DeleteRow {
  name: string
  use: () => DeleteProbe
  api: ReturnType<typeof vi.fn>
  successTitle: string
  errorTitle: string
  keys: unknown[][]
}

function probe(m: { isSuccess: boolean; isError: boolean }, fire: () => void): DeleteProbe {
  return { fire, isSuccess: m.isSuccess, isError: m.isError }
}

const DELETE_ROWS: DeleteRow[] = [
  {
    name: 'useDeleteImage',
    use: () => {
      const m = useDeleteImage()
      return probe(m, () => m.mutate({ id: 'sha256:abc', force: false }))
    },
    api: mockDeleteImage,
    successTitle: 'Image removed',
    errorTitle: 'Failed to remove image',
    keys: [['resources', 'images'], ['dashboard-stats'], ['resources', 'cleanup-preview']],
  },
  {
    name: 'useDeleteVolume',
    use: () => {
      const m = useDeleteVolume()
      return probe(m, () => m.mutate({ name: 'vol-a', force: false }))
    },
    api: mockDeleteVolume,
    successTitle: 'Volume removed',
    errorTitle: 'Failed to remove volume',
    keys: [['resources', 'volumes']],
  },
  {
    name: 'useDeleteNetwork',
    use: () => {
      const m = useDeleteNetwork()
      return probe(m, () => m.mutate('net-1'))
    },
    api: mockDeleteNetwork,
    successTitle: 'Network removed',
    errorTitle: 'Failed to remove network',
    keys: [['resources', 'networks']],
  },
]

describe.each(DELETE_ROWS)('$name — useActionMutation contract', (row) => {
  function run(qc: QueryClient) {
    const { result } = renderHook(row.use, { wrapper: wrapper(qc) })
    act(() => { result.current.fire() })
    return result
  }

  it('keeps its success toast text and invalidates exactly its keys', async () => {
    row.api.mockResolvedValue({ outcome: 'success', reason: 'backend wording' })
    const qc = makeClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    const result = run(qc)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(mockToastSuccess).toHaveBeenCalledTimes(1)
    expect(mockToastSuccess).toHaveBeenCalledWith(row.successTitle)
    expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual(row.keys)
  })

  it('titles a rejected failed result with the action, reason as description', async () => {
    row.api.mockRejectedValue({ outcome: 'failed', reason: 'daemon said no', status: 500 })
    const result = run(makeClient())
    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(mockToastError).toHaveBeenCalledTimes(1)
    expect(mockToastError).toHaveBeenCalledWith(row.errorTitle, { description: 'daemon said no' })
  })

  it('keeps the WARNING level for a rejected partial result', async () => {
    row.api.mockRejectedValue({ outcome: 'partial', reason: 'half done', status: 409 })
    const result = run(makeClient())
    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(mockToastWarning).toHaveBeenCalledTimes(1)
    expect(mockToastWarning).toHaveBeenCalledWith('half done')
    expect(mockToastError).not.toHaveBeenCalled()
  })

  it('titles a non-ActionResult rejection with the action', async () => {
    row.api.mockRejectedValue(new Error('socket hang up'))
    const result = run(makeClient())
    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(mockToastError).toHaveBeenCalledTimes(1)
    expect(mockToastError.mock.calls[0][0]).toBe(row.errorTitle)
  })
})
