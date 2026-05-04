import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

const mockCreate = vi.fn()
const mockToastSuccess = vi.fn()
const mockToastError = vi.fn()
const mockToastWarning = vi.fn()

vi.mock('@/lib/api', () => ({
  stacksApi: {
    create: (...args: unknown[]) => mockCreate(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    warning: (...args: unknown[]) => mockToastWarning(...args),
  },
}))

import { useCreateStack } from '../useCreateStack'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useCreateStack', () => {
  it('calls stacksApi.create with input', async () => {
    mockCreate.mockResolvedValue({ lintResults: [] })
    const wrapper = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate({
      name: 'test-stack',
      composeContent: 'services:\n  web:\n    image: nginx',
      deploy: true,
    })

    await waitFor(() => expect(mockCreate).toHaveBeenCalled())
  })

  it('shows success toast when no lint issues', async () => {
    mockCreate.mockResolvedValue({ lintResults: [] })
    const wrapper = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate({
      name: 'test-stack',
      composeContent: 'services:\n  web:\n    image: nginx',
      deploy: false,
    })

    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith('Stack created successfully'))
  })

  it('shows warning toast when lint warnings', async () => {
    mockCreate.mockResolvedValue({ lintResults: [{ level: 'warning', message: 'test' }] })
    const wrapper = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate({
      name: 'test-stack',
      composeContent: 'services:\n  web:\n    image: nginx',
      deploy: false,
    })

    await waitFor(() => expect(mockToastWarning).toHaveBeenCalledWith('Stack created but has lint warnings'))
  })

  it('shows error toast on failure', async () => {
    mockCreate.mockRejectedValue({ error: 'Something went wrong' })
    const wrapper = createWrapper()
    const { result } = renderHook(() => useCreateStack(), { wrapper })

    result.current.mutate({
      name: 'test-stack',
      composeContent: 'services:\n  web:\n    image: nginx',
      deploy: false,
    })

    await waitFor(() => expect(mockToastError).toHaveBeenCalled())
  })
})
