import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { toast } from 'sonner'
import { useUpdateScanStore } from '@/stores/updateScanStore'

// Capture the onMessage callback handed to the WS hook so tests can drive events.
let capturedOnMessage: ((data: unknown) => void) | null = null

vi.mock('../useWebSocket', () => ({
  useWebSocketJSON: (_path: string, onMessage: (d: unknown) => void) => {
    capturedOnMessage = onMessage
    return { lastMessage: null, status: 'open', send: vi.fn() }
  },
}))

vi.mock('@/lib/query-client', () => ({
  queryClient: { invalidateQueries: vi.fn(), setQueryData: vi.fn() },
}))

vi.mock('@/lib/api', () => ({
  resourcesApi: { checkUpdates: vi.fn() },
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: { loading: vi.fn(), success: vi.fn(), error: vi.fn(), dismiss: vi.fn() },
}))

import { useStackEvents } from '../useStackEvents'
import { UPDATE_SCAN_TOAST_ID } from '../useResources'

beforeEach(() => {
  capturedOnMessage = null
  useUpdateScanStore.setState({ isScanning: false })
  vi.clearAllMocks()
})

describe('useStackEvents update-scan completion', () => {
  it('finishes the scan and shows success on update_scan_complete', () => {
    useUpdateScanStore.setState({ isScanning: true })
    renderHook(() => useStackEvents())

    expect(capturedOnMessage).toBeTypeOf('function')
    capturedOnMessage!({ type: 'update_scan_complete', timestamp: '' })

    expect(useUpdateScanStore.getState().isScanning).toBe(false)
    expect(toast.success).toHaveBeenCalledWith('Update check complete', {
      id: UPDATE_SCAN_TOAST_ID,
      duration: 3000,
    })
  })

  it('finishes the scan and shows an error toast on update_scan_failed', () => {
    useUpdateScanStore.setState({ isScanning: true })
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_scan_failed', timestamp: '' })

    expect(useUpdateScanStore.getState().isScanning).toBe(false)
    expect(toast.error).toHaveBeenCalledWith(
      'Update check failed',
      expect.objectContaining({ id: UPDATE_SCAN_TOAST_ID }),
    )
  })

  it('does not toast when a background scan completes with no user-initiated scan', () => {
    // isScanning stays false — the user never clicked Check.
    renderHook(() => useStackEvents())

    capturedOnMessage!({ type: 'update_scan_complete', timestamp: '' })

    expect(toast.success).not.toHaveBeenCalled()
    expect(useUpdateScanStore.getState().isScanning).toBe(false)
  })
})
