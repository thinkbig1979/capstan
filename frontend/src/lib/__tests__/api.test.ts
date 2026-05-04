import { describe, it, expect } from 'vitest'

describe('api module', () => {
  it('exports apiClient as axios instance', async () => {
    const { apiClient } = await import('../api')
    expect(apiClient).toBeDefined()
    expect(apiClient.defaults.baseURL).toContain('/api/v1')
  })

  it('exports authApi with expected methods', async () => {
    const { authApi } = await import('../api')
    expect(authApi).toBeDefined()
    expect(typeof authApi.login).toBe('function')
    expect(typeof authApi.logout).toBe('function')
    expect(typeof authApi.me).toBe('function')
    expect(typeof authApi.status).toBe('function')
    expect(typeof authApi.setup).toBe('function')
  })

  it('exports stacksApi with expected methods', async () => {
    const { stacksApi } = await import('../api')
    expect(stacksApi).toBeDefined()
    expect(typeof stacksApi.list).toBe('function')
    expect(typeof stacksApi.get).toBe('function')
    expect(typeof stacksApi.start).toBe('function')
    expect(typeof stacksApi.stop).toBe('function')
    expect(typeof stacksApi.restart).toBe('function')
    expect(typeof stacksApi.pull).toBe('function')
    expect(typeof stacksApi.create).toBe('function')
  })

  it('exports gitApi with expected methods', async () => {
    const { gitApi } = await import('../api')
    expect(gitApi).toBeDefined()
    expect(typeof gitApi.status).toBe('function')
    expect(typeof gitApi.pull).toBe('function')
    expect(typeof gitApi.log).toBe('function')
    expect(typeof gitApi.diff).toBe('function')
  })

  it('exports resourcesApi with expected methods', async () => {
    const { resourcesApi } = await import('../api')
    expect(resourcesApi).toBeDefined()
    expect(typeof resourcesApi.images).toBe('function')
    expect(typeof resourcesApi.volumes).toBe('function')
    expect(typeof resourcesApi.networks).toBe('function')
    expect(typeof resourcesApi.buildCache).toBe('function')
  })

  it('exports settingsApi with expected methods', async () => {
    const { settingsApi } = await import('../api')
    expect(settingsApi).toBeDefined()
    expect(typeof settingsApi.getUpdates).toBe('function')
    expect(typeof settingsApi.getConfig).toBe('function')
  })

  it('exports setAuthCallbacks function', async () => {
    const { setAuthCallbacks } = await import('../api')
    expect(typeof setAuthCallbacks).toBe('function')
  })
})
