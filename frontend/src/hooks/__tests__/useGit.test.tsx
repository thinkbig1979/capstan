import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { ActionResult } from '@/lib/action-result'

// ─── Mocks ───────────────────────────────────────────────────────────────────

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
  },
}))

const mockGitStatus = vi.fn()
const mockGitLog = vi.fn()
const mockGitDiff = vi.fn()
const mockGitPull = vi.fn()

vi.mock('@/lib/api', () => ({
  gitApi: {
    status: (...args: unknown[]) => mockGitStatus(...args),
    log: (...args: unknown[]) => mockGitLog(...args),
    diff: (...args: unknown[]) => mockGitDiff(...args),
    pull: (...args: unknown[]) => mockGitPull(...args),
  },
}))

import { toast } from 'sonner'
import { useGitStatus, useGitLog, useGitDiff, useGitPull, normalisePullResult } from '../useGit'

// ─── Helpers ─────────────────────────────────────────────────────────────────

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─── Existing query hooks ─────────────────────────────────────────────────────

describe('useGitStatus', () => {
  it('calls gitApi.status with stackId', async () => {
    mockGitStatus.mockResolvedValue({ branch: 'main', ahead: 0, behind: 0 })
    const wrapper = createWrapper()
    renderHook(() => useGitStatus('stack1'), { wrapper })
    await waitFor(() => expect(mockGitStatus).toHaveBeenCalledWith('stack1'))
  })
})

describe('useGitLog', () => {
  it('calls gitApi.log with params', async () => {
    mockGitLog.mockResolvedValue([])
    const wrapper = createWrapper()
    renderHook(() => useGitLog('stack1', 10, 0), { wrapper })
    await waitFor(() => expect(mockGitLog).toHaveBeenCalledWith('stack1', 10, 0, undefined))
  })
})

describe('useGitDiff', () => {
  it('does not fetch when hash is empty', () => {
    mockGitDiff.mockResolvedValue('')
    const wrapper = createWrapper()
    renderHook(() => useGitDiff('stack1', ''), { wrapper })
    expect(mockGitDiff).not.toHaveBeenCalled()
  })

  it('fetches when hash is provided', async () => {
    mockGitDiff.mockResolvedValue('diff content')
    const wrapper = createWrapper()
    renderHook(() => useGitDiff('stack1', 'abc123'), { wrapper })
    await waitFor(() => expect(mockGitDiff).toHaveBeenCalledWith('stack1', 'abc123'))
  })
})

// ─── normalisePullResult unit tests ──────────────────────────────────────────

describe('normalisePullResult', () => {
  it('passes through ActionResult shape unchanged', () => {
    const ar: ActionResult = { outcome: 'success', reason: 'HEAD advanced' }
    expect(normalisePullResult(ar as any)).toEqual(ar)
  })

  it('maps legacy success with different commits → success', () => {
    const result = normalisePullResult({
      success: true,
      previousCommit: 'abc1234',
      currentCommit: 'def5678',
      changedFiles: ['docker-compose.yml'],
      redeployedStacks: [],
    } as any)
    expect(result.outcome).toBe('success')
    expect(result.reason).toContain('abc1234')
    expect(result.reason).toContain('def5678')
  })

  it('maps legacy success with same commit → no_change', () => {
    const result = normalisePullResult({
      success: true,
      previousCommit: 'abc1234',
      currentCommit: 'abc1234',
      changedFiles: [],
      redeployedStacks: [],
    } as any)
    expect(result.outcome).toBe('no_change')
    expect(result.reason).toBe('Already up to date')
  })

  it('maps legacy success:false → failed', () => {
    const result = normalisePullResult({
      success: false,
      previousCommit: '',
      currentCommit: '',
      changedFiles: [],
      redeployedStacks: [],
    } as any)
    expect(result.outcome).toBe('failed')
  })
})

// ─── useGitPull — Action Truth Contract (B4, finding #9) ─────────────────────

describe('useGitPull — ActionResult outcomes', () => {
  it('success outcome → toast.success, NOT toast.info/warning', async () => {
    const ar: ActionResult = { outcome: 'success', reason: 'HEAD advanced to abc1234' }
    mockGitPull.mockResolvedValue(ar)

    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'stack1', redeploy: false })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toast.success).toHaveBeenCalledWith('HEAD advanced to abc1234')
    expect(toast.info).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('no_change outcome → toast.info (already up to date), NOT green success', async () => {
    const ar: ActionResult = { outcome: 'no_change', reason: 'Already up to date' }
    mockGitPull.mockResolvedValue(ar)

    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'stack1' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toast.info).toHaveBeenCalledWith('Already up to date')
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('partial outcome → toast.warning (not green), lists failed redeploys', async () => {
    const ar: ActionResult<{
      failedRedeploys: Array<{ stack: string; reason: string }>
    }> = {
      outcome: 'partial',
      reason: 'Pull succeeded but redeploy failed',
      details: {
        failedRedeploys: [
          { stack: 'myapp', reason: 'container exited with code 1' },
          { stack: 'worker', reason: 'image pull error' },
        ],
      },
    }
    mockGitPull.mockResolvedValue(ar)

    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'stack1', redeploy: true })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    // Should warn (not succeed)
    expect(toast.warning).toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()

    // Should list the failed redeploy stacks
    const warnCalls = (toast.warning as ReturnType<typeof vi.fn>).mock.calls.flat()
    const warnText = warnCalls.join(' ')
    expect(warnText).toContain('myapp')
    expect(warnText).toContain('worker')
  })

  it('failed outcome → toast.error, not green', async () => {
    const ar: ActionResult = { outcome: 'failed', reason: 'Merge conflict detected' }
    mockGitPull.mockResolvedValue(ar)

    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'stack1' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toast.error).toHaveBeenCalledWith('Merge conflict detected')
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
  })

  it('legacy success with same commit → info (no_change), not green success', async () => {
    mockGitPull.mockResolvedValue({
      success: true,
      previousCommit: 'abc1234',
      currentCommit: 'abc1234',
      changedFiles: [],
      redeployedStacks: [],
    })

    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'stack1' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toast.info).toHaveBeenCalledWith('Already up to date')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('legacy success with advancing commit → success toast', async () => {
    mockGitPull.mockResolvedValue({
      success: true,
      previousCommit: 'aaa0001',
      currentCommit: 'bbb0002',
      changedFiles: ['docker-compose.yml'],
      redeployedStacks: ['myapp'],
    })

    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'stack1' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(toast.success).toHaveBeenCalled()
    expect(toast.info).not.toHaveBeenCalled()
    expect(toast.warning).not.toHaveBeenCalled()
  })

  it('passes stackId and redeploy to gitApi.pull', async () => {
    mockGitPull.mockResolvedValue({ outcome: 'success', reason: 'done' })
    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })

    act(() => {
      result.current.mutate({ stackId: 'my-stack', redeploy: true })
    })

    await waitFor(() => expect(mockGitPull).toHaveBeenCalledWith('my-stack', true))
  })
})
