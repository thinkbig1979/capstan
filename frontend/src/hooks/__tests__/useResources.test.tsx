import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { useUpdateScanStore } from '@/stores/updateScanStore'

const mockCheckUpdates = vi.fn()
const mockCreateNetwork = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    checkUpdates: (...args: unknown[]) => mockCheckUpdates(...args),
    createNetwork: (...args: unknown[]) => mockCreateNetwork(...args),
  },
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: { loading: vi.fn(), success: vi.fn(), error: vi.fn(), dismiss: vi.fn() },
}))

import {
  useCheckUpdates,
  useCheckUpdatesRefresh,
  useCreateNetwork,
  useUpdateScanWatcher,
  UPDATE_SCAN_TOAST_ID,
} from '../useResources'
import { queryKeys } from '@/lib/query-keys'

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
    queryClient.setQueryData(queryKeys.resources.updates(), { updates: [], scanning: false, scannedAt: baseline })
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
    queryClient.setQueryData(queryKeys.resources.updates(), { updates: [], scanning: false, scannedAt: baseline })
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

// The manual "Check for updates" refresh (agent-os-82lk). Its onError used to be
// zero-arity and render a fixed 'Update check failed' for every failure, so the
// one actionable code the route can answer with was thrown away.
describe('useCheckUpdatesRefresh', () => {
  // respond.go's DockerUnavailableMessage, verbatim.
  const DOCKER_UNAVAILABLE_MESSAGE =
    'Docker daemon unreachable: the server started without a usable Docker connection. Check that the Docker socket is mounted and the daemon is running, then restart Capstan.'

  it('names the Docker outage on a 503 DOCKER_UNAVAILABLE', async () => {
    mockCheckUpdates.mockRejectedValue({
      status: 503,
      code: 'DOCKER_UNAVAILABLE',
      message: DOCKER_UNAVAILABLE_MESSAGE,
    })

    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCheckUpdatesRefresh(), { wrapper })
    act(() => result.current.mutate())

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.error).toHaveBeenCalledWith('Update check failed', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 4000,
      description: DOCKER_UNAVAILABLE_MESSAGE,
    })
    expect(useUpdateScanStore.getState().isScanning).toBe(false)
  })

  // The negative arm, and it is a REGRESSION GUARD rather than a fail-first one:
  // before this change every failure rendered the bare sentence, so this was
  // already green. It pins that nothing outside the allow-list reaches the toast.
  //
  // The producer here is a TRANSPORT failure, not a server-minted error code,
  // and that is deliberate: GET /resources/updates?refresh=true answers 202 or
  // 503 DOCKER_UNAVAILABLE and nothing else on the production path, so mocking a
  // different server code would pin a response the route cannot produce. This
  // body is the shape lib/api.ts's interceptor builds when there is no response
  // at all — axios's own code and message, never the server's.
  it('keeps the generic sentence on a transport failure', async () => {
    mockCheckUpdates.mockRejectedValue({
      error: 'Unknown error',
      code: 'ECONNABORTED',
      message: 'timeout of 120000ms exceeded',
    })

    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCheckUpdatesRefresh(), { wrapper })
    act(() => result.current.mutate())

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.error).toHaveBeenCalledWith('Update check failed', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 4000,
    })
    expect(useUpdateScanStore.getState().isScanning).toBe(false)
  })
})

// ─── agent-os-06c1: unchecked-cast narrowing ─────────────────────────────────

describe('useCheckUpdatesRefresh — the fault message is asserted, not validated', () => {
  it('falls back to the bare sentence when message is not a string', async () => {
    // updateScanFault declares `string | null` and reaches it through
    // `error as { code?: string; message?: string }`. A non-string message
    // would land in sonner's `description`, typed ReactNode, inside the
    // Toaster subtree.
    mockCheckUpdates.mockRejectedValue({
      status: 503,
      code: 'DOCKER_UNAVAILABLE',
      message: { detail: 'not a sentence' },
    })

    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCheckUpdatesRefresh(), { wrapper })
    act(() => result.current.mutate())

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.error).toHaveBeenCalledWith('Update check failed', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 4000,
    })
  })
})

describe('useCreateNetwork — details.name is asserted, not validated', () => {
  it('does not render a non-string network name into the success toast', async () => {
    mockCreateNetwork.mockResolvedValue({
      outcome: 'success',
      reason: 'Network created',
      details: { name: { value: 'bridge-1' } },
    })

    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCreateNetwork(), { wrapper })
    act(() => result.current.mutate({ name: 'bridge-1' }))

    await waitFor(() => expect(toast.success).toHaveBeenCalled())
    const said = (toast.success as ReturnType<typeof vi.fn>).mock.calls.flat().join(' ')
    expect(said).not.toContain('[object Object]')
  })

  it('still renders a real string name, so nothing reachable moved', async () => {
    mockCreateNetwork.mockResolvedValue({
      outcome: 'success',
      reason: 'Network created',
      details: { name: 'bridge-1' },
    })

    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCreateNetwork(), { wrapper })
    act(() => result.current.mutate({ name: 'bridge-1' }))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Network "bridge-1" created'))
  })
})
