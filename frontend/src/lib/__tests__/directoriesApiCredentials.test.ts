import { describe, it, expect, vi, beforeEach } from 'vitest'

// Capture the axios instance api.ts creates so we can spy on its verbs.
// vi.hoisted runs before the hoisted vi.mock factory, so `instance` is defined.
const instance = vi.hoisted(() => ({
  get: vi.fn().mockResolvedValue({ data: {} }),
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

import { directoriesApi } from '@/lib/api'

beforeEach(() => {
  instance.put.mockClear()
})

describe('directoriesApi.updateCredentials', () => {
  // Regression: agent-os-p7r. The backend only ever registered
  // PUT /directories/credentials (a static route reading `path` from the
  // JSON body) — there is no `/:path/credentials` route, and gin's decoded
  // wildcard matching can't route an absolute, slash-containing directory
  // path through a single URL segment anyway. The old client put the path in
  // the URL and left it out of the body, so every save 404'd. The path must
  // travel in the JSON body, and the URL must stay a bare, path-free string.
  it('PUTs to /directories/credentials with the directory path in the body', async () => {
    await directoriesApi.updateCredentials('/opt/stacks/app', {
      authType: 'https',
      httpsUser: 'git',
      httpsToken: 'ghp_secret',
    })

    expect(instance.put).toHaveBeenCalledTimes(1)
    const [url, body] = instance.put.mock.calls[0] as [string, Record<string, unknown>]

    // The URL must be the bare, static route the backend actually registers.
    expect(url).toBe('/directories/credentials')

    // The (possibly slash-containing) directory path must ride in the body,
    // not the URL, alongside the rest of the credential fields.
    expect(body).toEqual({
      path: '/opt/stacks/app',
      authType: 'https',
      httpsUser: 'git',
      httpsToken: 'ghp_secret',
    })
  })
})
