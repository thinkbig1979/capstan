import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

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

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

import { useGitStatus, useGitLog, useGitDiff, useGitPull } from '../useGit'

beforeEach(() => {
  vi.clearAllMocks()
})

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

describe('useGitPull', () => {
  it('calls gitApi.pull with stackId and redeploy', async () => {
    mockGitPull.mockResolvedValue({ success: true })
    const wrapper = createWrapper()
    const { result } = renderHook(() => useGitPull(), { wrapper })
    result.current.mutate({ stackId: 'stack1', redeploy: true })
    await waitFor(() => expect(mockGitPull).toHaveBeenCalledWith('stack1', true))
  })
})
