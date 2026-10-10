import { describe, it, expect, vi } from 'vitest'

// Same axios capture as backupApiHistory.test.ts.
const instance = vi.hoisted(() => ({
  get: vi.fn().mockResolvedValue({ data: {} }),
  post: vi.fn().mockResolvedValue({ data: { runId: 'r1', wsUrl: '/ws/backups/verify/r1' } }),
  put: vi.fn().mockResolvedValue({ data: {} }),
  delete: vi.fn().mockResolvedValue({ data: {} }),
  interceptors: {
    request: { use: vi.fn() },
    response: { use: vi.fn() },
  },
}))

vi.mock('axios', () => ({
  default: { create: () => instance },
  AxiosError: class AxiosError extends Error {},
}))

import { backupApi } from '@/lib/api'

/**
 * agent-os-ffaj, D68.2: the check's depth is not exposed, so the request
 * carries no readDataSubset and the server's default (5%) applies.
 */
describe('backupApi.runVerify', () => {
  it('POSTs /backups/verify with no readDataSubset and returns the run handle', async () => {
    const result = await backupApi.runVerify()

    expect(instance.post).toHaveBeenCalledTimes(1)
    const [url, body] = instance.post.mock.calls[0] as [string, Record<string, unknown>]
    expect(url).toBe('/backups/verify')
    expect(body).toEqual({})
    expect(result).toEqual({ runId: 'r1', wsUrl: '/ws/backups/verify/r1' })
  })
})
