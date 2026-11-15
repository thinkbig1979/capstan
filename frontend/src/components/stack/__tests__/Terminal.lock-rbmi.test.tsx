import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { screen, act, fireEvent, waitFor } from '@testing-library/react'
import { renderWithProviders as render } from '@/test/utils'
import type { Stack } from '@/types'
import type { UseWebSocketOptions } from '@/hooks/useWebSocket'

// agent-os-rbmi: a shell shows a container's env values, so opening a
// terminal needs the env unlock, and locking closes an open one.

window.HTMLElement.prototype.scrollIntoView = vi.fn()
beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false, media: query, onchange: null,
      addListener: () => {}, removeListener: () => {},
      addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
    }),
  })
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

let capturedOptions: UseWebSocketOptions | null = null
const sendSpy = vi.fn()
const disconnectSpy = vi.fn()

vi.mock('@/hooks/useWebSocket', () => ({
  useWebSocketBinary: (_path: string, _onMessage: unknown, options: UseWebSocketOptions) => {
    capturedOptions = options
    return { send: sendSpy, disconnect: disconnectSpy, reconnect: vi.fn() }
  },
}))

import { TerminalComponent } from '../Terminal'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/authStore'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

function makeStack(): Stack {
  return {
    envFile: '', gitBranch: '', gitCommit: '', id: 'stack1', directory: '/srv/stack1',
    composeFile: 'docker-compose.yml', projectName: 'stack1', status: 'running',
    isGitRepo: false, gitDirty: false, gitAhead: 0, gitBehind: 0,
    containers: [{ health: '', id: 'c1', name: 'web', image: 'nginx', state: 'running', status: 'Up', ports: [] }],
  }
}

const UNLOCK_PROMPT = /Re-enter your password to open a terminal/

beforeEach(() => {
  capturedOptions = null
  sendSpy.mockClear()
  disconnectSpy.mockClear()
  vi.mocked(toast.error).mockClear()
  localStorage.clear()
  useAuthStore.setState({ authDisabled: false })
  useEnvUnlockStore.getState().lock()
})

afterEach(() => {
  useEnvUnlockStore.getState().lock()
})

describe('TerminalComponent — env lock', () => {
  it('does not dial while locked, and offers Unlock', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    expect(capturedOptions?.skip).toBe(true)
    expect(screen.getByText(UNLOCK_PROMPT)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Unlock' }))
    expect(screen.getByText('Unlock environment variables')).toBeInTheDocument()
  })

  it('dials once unlocked, sending the token, and does not redial a refused handshake', () => {
    act(() => useEnvUnlockStore.getState().unlock('tok-1'))
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    expect(capturedOptions?.skip).toBe(false)
    expect(capturedOptions?.sendUnlockToken).toBe(true)
    expect(capturedOptions?.redialIfNeverOpened).toBe(false)
    expect(screen.queryByText(UNLOCK_PROMPT)).not.toBeInTheDocument()
  })

  it('CONTROL: with auth disabled there is no lock to open', () => {
    useAuthStore.setState({ authDisabled: true })
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    expect(capturedOptions?.skip).toBe(false)
    expect(screen.queryByText(UNLOCK_PROMPT)).not.toBeInTheDocument()
  })

  it('closes an open terminal when the session locks', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok-1'))
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    act(() => capturedOptions?.onOpen?.())
    expect(screen.getByText('Connected')).toBeInTheDocument()

    act(() => useEnvUnlockStore.getState().lock())

    expect(disconnectSpy).toHaveBeenCalled()
    expect(capturedOptions?.skip).toBe(true)
    await waitFor(() => expect(screen.queryByText('Connected')).not.toBeInTheDocument())
    expect(screen.getByText(UNLOCK_PROMPT)).toBeInTheDocument()
  })

  it('renews an open terminal in-band when the tab unlocks again', () => {
    act(() => useEnvUnlockStore.getState().unlock('tok-1'))
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    act(() => capturedOptions?.onOpen?.())
    sendSpy.mockClear()

    act(() => useEnvUnlockStore.getState().unlock('tok-2'))

    expect(sendSpy).toHaveBeenCalledWith(JSON.stringify({ type: 'unlock', token: 'tok-2' }))
    expect(disconnectSpy).not.toHaveBeenCalled()
  })

  it('CONTROL: an unlock while not connected sends nothing', () => {
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    act(() => useEnvUnlockStore.getState().unlock('tok-1'))
    expect(sendSpy).not.toHaveBeenCalledWith(expect.stringContaining('"unlock"'))
  })

  it('says why when the server closes with 4403', async () => {
    act(() => useEnvUnlockStore.getState().unlock('tok-1'))
    render(<TerminalComponent stack={makeStack()} initialContainer="c1" />)
    act(() => capturedOptions?.onOpen?.())
    act(() => capturedOptions?.onClose?.(new CloseEvent('close', { code: 4403, reason: 'Unlock required' })))
    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Terminal closed: environment variables are locked')
    })
  })
})
