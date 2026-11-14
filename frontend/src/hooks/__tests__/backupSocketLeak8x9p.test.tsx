/**
 * agent-os-8x9p: useBackupStreaming must close its WSClient on unmount, reset
 * and a replacing connect() even after the socket dropped. WSClient redials a
 * dropped socket and re-arms on online/visibility, so a client the hook stops
 * tracking keeps dialling after the page is gone.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useAuthStore } from '@/stores/authStore'

type WSOptions = {
  onOpen?: () => void
  onClose?: () => void
  onError?: (e: Event) => void
  onReconnectFailed?: () => void
}

// One record per client, so a test can tell WHICH client was closed.
const clients: { close: ReturnType<typeof vi.fn>; options: WSOptions }[] = []

vi.mock('@/lib/ws', () => {
  class MockWSClient {
    close = vi.fn()
    connect(_path: string, _onMessage: unknown, options: WSOptions = {}) {
      clients.push({ close: this.close, options })
    }
  }
  return { WSClient: MockWSClient }
})

const mockInvalidateQueries = vi.fn()

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQueryClient: () => ({ invalidateQueries: mockInvalidateQueries }),
  }
})

import { useBackupStreaming } from '../useBackup'

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>
}

beforeEach(() => {
  vi.clearAllMocks()
  clients.length = 0
  useAuthStore.setState({ authDisabled: true, isAuthenticated: false, token: null })
})

describe('agent-os-8x9p: a dropped backup socket is still closed by its owner', () => {
  it.each([
    ['a drop (onClose)', (o: WSOptions) => o.onClose?.()],
    ['an error event (onError)', (o: WSOptions) => o.onError?.(new Event('error'))],
    ['an exhausted redial ladder (onReconnectFailed)', (o: WSOptions) => o.onReconnectFailed?.()],
  ])('unmount closes the client after %s', (_name, drop) => {
    const { result, unmount } = renderHook(() => useBackupStreaming(), { wrapper })
    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { drop(clients[0].options) })

    unmount()

    expect(clients[0].close).toHaveBeenCalledTimes(1)
  })

  it('reset() closes the client after a drop', () => {
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { clients[0].options.onClose?.() })

    act(() => { result.current.reset() })

    expect(clients[0].close).toHaveBeenCalledTimes(1)
  })

  it('a replacing connect() closes the dropped client, not the new one', () => {
    const { result } = renderHook(() => useBackupStreaming(), { wrapper })
    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { clients[0].options.onClose?.() })

    act(() => { result.current.connect('/ws/backups/run/def') })

    expect(clients[0].close).toHaveBeenCalledTimes(1)
    expect(clients[1].close).not.toHaveBeenCalled()
  })

  // The drop already refetched (reconcileOnClose). A redial that re-attaches
  // makes the run live again, so leaving it mid-run refetches once more.
  it('unmount after a drop and a re-attach refetches; after a drop alone it does not refetch twice', () => {
    const { result, unmount } = renderHook(() => useBackupStreaming(), { wrapper })
    act(() => { result.current.connect('/ws/backups/run/abc') })
    act(() => { clients[0].options.onClose?.() })
    expect(mockInvalidateQueries).toHaveBeenCalledTimes(2)

    act(() => { clients[0].options.onOpen?.() })
    unmount()

    expect(mockInvalidateQueries).toHaveBeenCalledTimes(4)
  })
})
