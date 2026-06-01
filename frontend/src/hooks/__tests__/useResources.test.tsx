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
  toast: { loading: vi.fn(), success: vi.fn(), error: vi.fn(), dismiss: vi.fn() },
}))

import { useCheckUpdates, useUpdateScanWatcher, UPDATE_SCAN_TOAST_ID } from '../useResources'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
    },
  })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return { wrapper, queryClient }
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

    const { wrapper } = createWrapper()
    renderHook(() => useCheckUpdates(), { wrapper })

    await waitFor(() => {
      expect(useUpdateScanStore.getState().isScanning).toBe(true)
    })
  })

  it('does NOT clear isScanning on a scanning:false poll (it only ever starts a scan)', async () => {
    // useCheckUpdates surfaces the indicator (startScan) but must never finish a
    // scan from a poll — completion is owned by useUpdateScanWatcher / the WS event.
    // A bare scanning:false response must leave an in-flight scan running.
    useUpdateScanStore.setState({ isScanning: true })

    mockCheckUpdates.mockResolvedValue({
      updates: [],
      fromCache: false,
      scanning: false,
    })

    const finishScanSpy = vi.spyOn(useUpdateScanStore.getState(), 'finishScan')

    const { wrapper } = createWrapper()
    renderHook(() => useCheckUpdates(), { wrapper })

    await waitFor(() => expect(mockCheckUpdates).toHaveBeenCalled())
    // Let the resolved scanning:false data propagate through the effect — the
    // buggy code finishes the scan here; the fixed code leaves it running.
    await act(async () => { await new Promise((r) => setTimeout(r, 20)) })

    expect(finishScanSpy).not.toHaveBeenCalled()
    expect(useUpdateScanStore.getState().isScanning).toBe(true)
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

    const { wrapper } = createWrapper()
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

    const { wrapper } = createWrapper()
    renderHook(() => useUpdateScanWatcher(), { wrapper })

    // Not scanning yet — no toast on mount.
    expect(toast.loading).not.toHaveBeenCalled()

    act(() => useUpdateScanStore.setState({ isScanning: true }))

    await waitFor(() => {
      expect(toast.loading).toHaveBeenCalledWith('Checking for updates…', { id: UPDATE_SCAN_TOAST_ID })
    })
  })

  it('does NOT finish on a stale poll carrying the pre-scan scannedAt baseline', async () => {
    // Reproduces the premature-finish bug: when isScanning flips true the shared
    // query can return a STALE cached scanning:false. Because that stale data still
    // carries the pre-scan scannedAt (the baseline), it must not end the scan.
    const baseline = '2026-01-01T00:00:00Z'
    const { wrapper, queryClient } = createWrapper()
    queryClient.setQueryData(['resources', 'updates'], { updates: [], scanning: false, scannedAt: baseline })
    useUpdateScanStore.setState({ isScanning: true })
    mockCheckUpdates.mockResolvedValue({ updates: [], scanning: false, scannedAt: baseline })

    renderHook(() => useUpdateScanWatcher(), { wrapper })

    await waitFor(() => expect(mockCheckUpdates).toHaveBeenCalled())
    await act(async () => { await new Promise((r) => setTimeout(r, 30)) })

    expect(useUpdateScanStore.getState().isScanning).toBe(true)
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('finishes the scan and shows success when a poll reports a newer scannedAt', async () => {
    // Reliable, WS-independent completion: the backend bumps scannedAt when a scan
    // genuinely finishes, so a fresh scanning:false poll with a newer scannedAt ends
    // the scan even if the WS completion event never arrives.
    const baseline = '2026-01-01T00:00:00Z'
    const { wrapper, queryClient } = createWrapper()
    queryClient.setQueryData(['resources', 'updates'], { updates: [], scanning: false, scannedAt: baseline })
    useUpdateScanStore.setState({ isScanning: true })
    mockCheckUpdates.mockResolvedValue({ updates: [], scanning: false, scannedAt: '2026-01-01T00:05:00Z' })

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
