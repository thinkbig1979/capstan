import { describe, it, expect, vi, beforeEach } from 'vitest'

// Capture the axios instance api.ts creates so we can spy on its verbs.
// vi.hoisted runs before the hoisted vi.mock factory, so `instance` is defined.
const instance = vi.hoisted(() => ({
  get: vi.fn().mockResolvedValue({ data: {} }),
  post: vi.fn().mockResolvedValue({ data: {} }),
  put: vi.fn().mockResolvedValue({ data: {} }),
  delete: vi.fn().mockResolvedValue({ data: { outcome: 'success', reason: 'stack deleted' } }),
  interceptors: {
    request: { use: vi.fn() },
    response: { use: vi.fn() },
  },
}))

vi.mock('axios', () => ({
  default: { create: () => instance },
  AxiosError: class AxiosError extends Error {},
}))

import { stacksApi } from '@/lib/api'

beforeEach(() => {
  instance.delete.mockClear()
})

describe('stacksApi.delete', () => {
  // Regression: the backend Delete handler returns 400 ("Confirmation required")
  // unless ?confirm=true is present. The client must send it — the UI already
  // gates the call behind its own confirmation dialog.
  it('requests the delete with confirm=true so the backend does not 400', async () => {
    await stacksApi.delete('stacks~dokemon:default')

    expect(instance.delete).toHaveBeenCalledTimes(1)
    const url = instance.delete.mock.calls[0][0] as string
    expect(url).toContain('confirm=true')
    // The stack id must still be encoded into the path.
    expect(url).toContain(encodeURIComponent('stacks~dokemon:default'))
  })

  // Regression: agent-os-7et. The second, distinct confirmation (after a 428
  // STACK_DELETE_COLLATERAL refusal) must add confirmCollateral=true, and only
  // then — an unconditional confirmCollateral=true would silently defeat lg2's
  // guard against destroying collateral files.
  it('adds confirmCollateral=true only when the caller explicitly passes it', async () => {
    await stacksApi.delete('my-stack', true)

    expect(instance.delete).toHaveBeenCalledTimes(1)
    const url = instance.delete.mock.calls[0][0] as string
    expect(url).toContain('confirm=true')
    expect(url).toContain('confirmCollateral=true')
  })

  it('omits confirmCollateral by default', async () => {
    await stacksApi.delete('my-stack')

    const url = instance.delete.mock.calls[0][0] as string
    expect(url).not.toContain('confirmCollateral')
  })
})
