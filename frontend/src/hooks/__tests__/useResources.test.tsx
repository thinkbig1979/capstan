import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { useUpdateScanStore } from '@/stores/updateScanStore'

const mockCheckUpdates = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    checkUpdates: (...args: unknown[]) => mockCheckUpdates(...args),
  },
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: { loading: vi.fn(), success: vi.fn() },
}))

import { useCheckUpdates, useUpdateScanWatcher, UPDATE_SCAN_TOAST_ID } from '../useResources'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  useUpdateScanStore.setState({ isScanning: false })
  vi.clearAllMocks()
})

describe('useCheckUpdates', () => {
  it('sets isScanning=true when response has scanning:true', async () => {
    mockCheckUpdates.mockResolvedValue({
      updates: [],
      fromCache: false,
      scanning: true,
    })

    const wrapper = createWrapper()
    renderHook(() => useCheckUpdates(), { wrapper })

    await waitFor(() => {
      expect(useUpdateScanStore.getState().isScanning).toBe(true)
    })
  })

  it('clears isScanning when response has scanning:false', async () => {
    // Start with isScanning=true so the finishScan branch is exercised.
    useUpdateScanStore.setState({ isScanning: true })

    mockCheckUpdates.mockResolvedValue({
      updates: [],
      fromCache: false,
      scanning: false,
    })

    const wrapper = createWrapper()
    renderHook(() => useCheckUpdates(), { wrapper })

    await waitFor(() => {
      expect(useUpdateScanStore.getState().isScanning).toBe(false)
    })
  })

  it('does not toggle when scanning value is unchanged', async () => {
    // Store already has isScanning=false; response also has scanning:false.
    // The effect guard (else if ... && isScanning) should prevent a no-op finishScan.
    useUpdateScanStore.setState({ isScanning: false })

    const startScanSpy = vi.spyOn(useUpdateScanStore.getState(), 'startScan')
    const finishScanSpy = vi.spyOn(useUpdateScanStore.getState(), 'finishScan')

    mockCheckUpdates.mockResolvedValue({
      updates: [],
      fromCache: false,
      scanning: false,
    })

    const wrapper = createWrapper()
    renderHook(() => useCheckUpdates(), { wrapper })

    // Wait for the query to resolve and the effect to fire.
    await waitFor(() => expect(mockCheckUpdates).toHaveBeenCalled())

    // Neither startScan nor finishScan should be called when values are already in sync.
    expect(startScanSpy).not.toHaveBeenCalled()
    expect(finishScanSpy).not.toHaveBeenCalled()
  })
})

describe('useUpdateScanWatcher', () => {
  it('shows a global loading toast when a scan starts', async () => {
    mockCheckUpdates.mockResolvedValue({ updates: [], scanning: true })

    const wrapper = createWrapper()
    renderHook(() => useUpdateScanWatcher(), { wrapper })

    // Not scanning yet — no toast on mount.
    expect(toast.loading).not.toHaveBeenCalled()

    act(() => useUpdateScanStore.setState({ isScanning: true }))

    await waitFor(() => {
      expect(toast.loading).toHaveBeenCalledWith('Checking for updates…', { id: UPDATE_SCAN_TOAST_ID })
    })
  })

  it('clears scanning and shows a success toast when the scan completes', async () => {
    // Scan already in flight; the next poll reports completion.
    useUpdateScanStore.setState({ isScanning: true })
    mockCheckUpdates.mockResolvedValue({ updates: [], scanning: false })

    const wrapper = createWrapper()
    renderHook(() => useUpdateScanWatcher(), { wrapper })

    await waitFor(() => {
      expect(useUpdateScanStore.getState().isScanning).toBe(false)
    })
    expect(toast.success).toHaveBeenCalledWith('Update check complete', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 3000,
    })
  })
})
