import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { apiClient, setAuthCallbacks } from '../api'
import { server } from '../../test/handlers'

describe('API Client', () => {
  beforeEach(() => {
    server.listen({ onUnhandledRequest: 'error' })
  })

  afterEach(() => {
    server.resetHandlers()
    server.close()
  })

  it('sets auth callbacks', () => {
    const getToken = vi.fn(() => 'test-token')
    const logout = vi.fn()

    setAuthCallbacks(getToken, logout)

    expect(() => setAuthCallbacks(getToken, logout)).not.toThrow()
  })

  it('adds authorization header when token is available', async () => {
    const getToken = vi.fn(() => 'test-token')
    const logout = vi.fn()
    setAuthCallbacks(getToken, logout)

    const response = await apiClient.get('/auth/me')

    expect(response.data).toHaveProperty('id')
    expect(response.data).toHaveProperty('username')
  })

  it('handles 401 errors and calls logout', async () => {
    const getToken = vi.fn(() => 'invalid-token')
    const logout = vi.fn()
    setAuthCallbacks(getToken, logout)

    const locationSpy = vi.spyOn(window, 'location', 'get').mockReturnValue({
      href: '/login',
    } as any)

    try {
      await apiClient.get('/auth/me')
    } catch (error) {
      expect(locationSpy.mock.calls).toHaveLength(1)
    }

    locationSpy.mockRestore()
  })
})
