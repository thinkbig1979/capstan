import { describe, it, expect, vi, beforeEach } from 'vitest'

// Capture the axios instance api.ts creates so we can assert on the params it
// sends. vi.hoisted runs before the hoisted vi.mock factory, so `instance` is
// defined by the time the factory runs. Same shape as stacksApiDelete.test.ts.
const instance = vi.hoisted(() => ({
  get: vi.fn().mockResolvedValue({
    data: { runs: [], total: 0, page: 1, limit: 50, totalPages: 0 },
  }),
  post: vi.fn().mockResolvedValue({ data: {} }),
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

beforeEach(() => {
  instance.get.mockClear()
})

/**
 * agent-os-lak4.2 widened backupApi.getHistory from `(limit)` to
 * `(limit | filters)`. The widening is additive: `useStackBackupRuns`
 * (hooks/useBackup.ts) is the one production caller and still passes a bare
 * number. R5 in the spec is exactly this call site breaking silently, so the
 * numeric arm is pinned here rather than left to type inference.
 */
describe('backupApi.getHistory', () => {
  it('keeps the numeric signature working: getHistory(20) sends only limit=20', async () => {
    await backupApi.getHistory(20)

    expect(instance.get).toHaveBeenCalledTimes(1)
    const [url, config] = instance.get.mock.calls[0] as [string, { params: Record<string, unknown> }]
    expect(url).toBe('/backups/history')
    expect(config.params).toEqual({ limit: 20 })
    // A `page` param would move the legacy caller off page 1 the moment the
    // default changed, so its ABSENCE is the assertion, not its value.
    expect('page' in config.params).toBe(false)
  })

  it('defaults to limit=50 when called with no arguments', async () => {
    await backupApi.getHistory()

    const [, config] = instance.get.mock.calls[0] as [string, { params: Record<string, unknown> }]
    expect(config.params).toEqual({ limit: 50 })
  })

  it('passes a filters object straight through as query params', async () => {
    await backupApi.getHistory({
      page: 2,
      limit: 25,
      status: 'failed',
      kind: 'backup',
      trigger: 'scheduled',
      from: '2026-09-01T00:00:00Z',
      to: '2026-09-07T00:00:00Z',
    })

    const [url, config] = instance.get.mock.calls[0] as [string, { params: Record<string, unknown> }]
    expect(url).toBe('/backups/history')
    expect(config.params).toEqual({
      page: 2,
      limit: 25,
      status: 'failed',
      kind: 'backup',
      trigger: 'scheduled',
      from: '2026-09-01T00:00:00Z',
      to: '2026-09-07T00:00:00Z',
    })
  })

  it('returns the paginated payload, not just runs', async () => {
    instance.get.mockResolvedValueOnce({
      data: { runs: [], total: 7, page: 2, limit: 25, totalPages: 1 },
    })

    const result = await backupApi.getHistory({ page: 2, limit: 25 })

    expect(result).toEqual({ runs: [], total: 7, page: 2, limit: 25, totalPages: 1 })
  })
})
