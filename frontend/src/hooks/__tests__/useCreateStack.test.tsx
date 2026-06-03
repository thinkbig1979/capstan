import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockCreate = vi.fn()
const mockToastSuccess = vi.fn()
const mockToastError = vi.fn()
const mockToastWarning = vi.fn()
const mockToastInfo = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    create: (...args: unknown[]) => mockCreate(...args),
  },
  // Needed so other imports from api don't fail
  resourcesApi: {},
  settingsApi: {},
  autoUpdateApi: {},
}))

vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    warning: (...args: unknown[]) => mockToastWarning(...args),
    info: (...args: unknown[]) => mockToastInfo(...args),
  },
}))

import { useCreateStack } from '../useCreateStack'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
    queryClient,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
})

const SAMPLE_INPUT = {
  name: 'test-stack',
  composeContent: 'services:\n  web:\n    image: nginx',
  deploy: true,
}

// ─── Basics + lint differentiation + failure paths ───────────────────────────

describe('useCreateStack — create call, lint differentiation, failure paths', () => {
  it('calls stacksApi.create with input', async () => {
    const stack = { id: 'abc~test:default', status: 'stopped' }
    mockCreate.mockResolvedValue({
      outcome: 'success',
      reason: 'Stack created',
      details: { stack, lintResults: [] },
    })
    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(mockCreate).toHaveBeenCalled())
  })

  it('shows warning toast when the success body carries lint warnings', async () => {
    const stack = { id: 'abc~test:default', status: 'stopped' }
    mockCreate.mockResolvedValue({
      outcome: 'success',
      reason: 'Stack created',
      details: { stack, lintResults: [{ level: 'warning', message: 'test' }], deployed: false },
    })
    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate({ ...SAMPLE_INPUT, deploy: false })

    await waitFor(() => expect(mockToastWarning).toHaveBeenCalledWith('Stack created but has lint warnings'))
  })

  it('shows an error toast on a 2xx failed outcome — never leaves the body silent', async () => {
    mockCreate.mockResolvedValue({ outcome: 'failed', reason: 'compose is invalid' })
    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('compose is invalid'))
    expect(mockToastSuccess).not.toHaveBeenCalled()
    expect(mockToastWarning).not.toHaveBeenCalled()
  })

  it('shows error toast on genuine failure (rejected, no stack in error body)', async () => {
    mockCreate.mockRejectedValue({ error: 'Something went wrong' })
    const { wrapper } = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(mockToastError).toHaveBeenCalled())
    expect(mockToastSuccess).not.toHaveBeenCalled()
    expect(mockToastWarning).not.toHaveBeenCalled()
  })
})

// ─── Action Truth Contract path: success outcome ──────────────────────────────

describe('useCreateStack — Action Truth Contract: success outcome', () => {
  it('shows success toast and invalidates stacks + directories', async () => {
    const stack = { id: 'abc~test:default', status: 'stopped' }
    // Backend shape: {outcome, reason, details:{stack, lintResults, deployed, deployOutput}}
    mockCreate.mockResolvedValue({
      outcome: 'success',
      reason: 'Stack created and deployed',
      details: { stack, lintResults: [], deployed: true },
    })
    const { wrapper, queryClient } = createWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockToastSuccess).toHaveBeenCalledWith('Stack created successfully')
    expect(mockToastError).not.toHaveBeenCalled()
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['directories'] })
  })
})

// ─── Action Truth Contract path: partial outcome (created-not-deployed) ───────
//
// This is the critical audit finding #14 case. The stack exists on disk and in
// the database; only the deploy step failed. The user must see a WARNING, not
// "Create failed". The stacks query must be invalidated so the stack appears.

describe('useCreateStack — partial outcome (created but not deployed)', () => {
  it('shows warning toast, NOT error, when outcome is partial (onSuccess path)', async () => {
    const stack = { id: 'abc~test:default', status: 'stopped' }
    // HTTP 207 arrives as onSuccess with outcome:'partial'
    // Backend shape: {outcome, reason, details:{stack, lintResults, deployed, deployError}}
    mockCreate.mockResolvedValue({
      outcome: 'partial',
      reason: 'Stack created but deploy failed: exit code 1',
      details: { stack, lintResults: [], deployed: false, deployError: 'exit code 1' },
    })
    const { wrapper, queryClient } = createWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    // Must show warning, never error or success
    expect(mockToastWarning).toHaveBeenCalledWith('Stack created but deploy failed: exit code 1')
    expect(mockToastSuccess).not.toHaveBeenCalled()
    expect(mockToastError).not.toHaveBeenCalled()

    // Must invalidate so the stack appears in the list
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['directories'] })
  })

  it('shows warning and invalidates stacks when partial arrives via onError (axios rejects 207)', async () => {
    // Some axios configurations reject 2xx non-200 responses. When the 207 body
    // contains both outcome:'partial' and a stack in details, it's a created-not-deployed
    // result — NOT a genuine create failure.
    // Backend response shape: {outcome:'partial', reason, details:{stack, lintResults, ...}}
    const stack = { id: 'abc~test:default', status: 'stopped' }
    mockCreate.mockRejectedValue({
      outcome: 'partial',
      reason: 'Deploy failed: container exited',
      details: { stack, lintResults: [], deployed: false },
    })
    const { wrapper, queryClient } = createWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(result.current.isError).toBe(true))

    // Must show warning, never "Create failed" error toast
    expect(mockToastWarning).toHaveBeenCalledWith('Deploy failed: container exited')
    expect(mockToastError).not.toHaveBeenCalled()
    expect(mockToastSuccess).not.toHaveBeenCalled()

    // Must still invalidate so the stack shows up
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['stacks'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['directories'] })
  })

  it('does NOT invalidate stacks on genuine create failure (no stack in error body)', async () => {
    // A genuine failure has no stack field; the directory was not created.
    mockCreate.mockRejectedValue({ error: 'Stack directory already exists' })
    const { wrapper, queryClient } = createWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate(SAMPLE_INPUT)

    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(mockToastError).toHaveBeenCalled()
    expect(mockToastWarning).not.toHaveBeenCalled()
    // stacks query must NOT be invalidated — no stack was created
    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['stacks'] })
  })
})
