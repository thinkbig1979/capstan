import { describe, it, expect } from 'vitest'
import { classifyError } from '../error-handler'

describe('classifyError', () => {
  it('returns unknown for null', () => {
    const result = classifyError(null)
    expect(result.type).toBe('unknown')
    expect(result.retryable).toBe(false)
  })

  it('returns unknown for undefined', () => {
    const result = classifyError(undefined)
    expect(result.type).toBe('unknown')
  })

  it('classifies ECONNABORTED as timeout', () => {
    const result = classifyError({ code: 'ECONNABORTED', message: 'timeout' })
    expect(result.type).toBe('timeout')
    expect(result.retryable).toBe(true)
  })

  it('classifies ETIMEDOUT as timeout', () => {
    const result = classifyError({ code: 'ETIMEDOUT', message: 'timed out' })
    expect(result.type).toBe('timeout')
  })

  it('classifies ERR_NETWORK as network', () => {
    const result = classifyError({ code: 'ERR_NETWORK', message: 'Network Error' })
    expect(result.type).toBe('network')
    expect(result.retryable).toBe(true)
  })

  it('classifies 401 as auth', () => {
    const result = classifyError({
      response: { status: 401, data: { error: 'Unauthorized' } },
      message: 'Unauthorized',
    })
    expect(result.type).toBe('auth')
    expect(result.retryable).toBe(false)
    expect(result.status).toBe(401)
  })

  it('classifies 403 as auth', () => {
    const result = classifyError({
      response: { status: 403, data: { error: 'Forbidden' } },
      message: 'Forbidden',
    })
    expect(result.type).toBe('auth')
    expect(result.status).toBe(403)
  })

  it('classifies 404 as server', () => {
    const result = classifyError({
      response: { status: 404, data: { error: 'Not Found' } },
      message: 'Not Found',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(false)
  })

  it('classifies 429 as server with retryable', () => {
    const result = classifyError({
      response: { status: 429, data: {} },
      message: 'Too Many Requests',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(true)
  })

  it('classifies 500 as server with retryable', () => {
    const result = classifyError({
      response: { status: 500, data: { error: 'Internal Server Error' } },
      message: 'Internal Server Error',
    })
    expect(result.type).toBe('server')
    expect(result.retryable).toBe(true)
    expect(result.status).toBe(500)
  })

  it('classifies 422 as validation', () => {
    const result = classifyError({
      response: {
        status: 422,
        data: { error: 'Validation failed', details: { name: 'is required' } },
      },
      message: 'Validation failed',
    })
    expect(result.type).toBe('validation')
    expect(result.retryable).toBe(false)
    expect(result.action).toBe('Fix')
  })

  it('classifies 400 as validation', () => {
    const result = classifyError({
      response: { status: 400, data: { error: 'Bad Request' } },
      message: 'Bad Request',
    })
    expect(result.type).toBe('validation')
    expect(result.retryable).toBe(false)
  })

  it('classifies message containing "network" as network', () => {
    const result = classifyError({ message: 'A network error occurred' })
    expect(result.type).toBe('network')
    expect(result.retryable).toBe(true)
  })

  it('classifies message containing "fetch failed" as network', () => {
    const result = classifyError({ message: 'TypeError: fetch failed' })
    expect(result.type).toBe('network')
  })

  it('returns unknown for unrecognized errors', () => {
    const result = classifyError({ message: 'something weird happened' })
    expect(result.type).toBe('unknown')
    expect(result.retryable).toBe(true)
  })

  it('extracts context from 404 details', () => {
    const result = classifyError({
      response: {
        status: 404,
        data: { error: 'Not found', details: { resource: 'stacks~myapp:default' } },
      },
      message: 'Not found',
    })
    expect(result.context).toBe('stacks~myapp:default')
  })
})
