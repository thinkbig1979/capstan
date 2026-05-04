import { describe, it, expect, beforeEach } from 'vitest'
import { useStackStore } from '../stackStore'

beforeEach(() => {
  useStackStore.getState().reset()
})

describe('stackStore initial state', () => {
  it('has null selectedStackId', () => {
    expect(useStackStore.getState().selectedStackId).toBeNull()
  })

  it('has overview as active tab', () => {
    expect(useStackStore.getState().activeTab).toBe('overview')
  })
})

describe('stackStore setSelectedStack', () => {
  it('sets selected stack id', () => {
    useStackStore.getState().setSelectedStack('stack-123')
    expect(useStackStore.getState().selectedStackId).toBe('stack-123')
  })

  it('clears selected stack id with null', () => {
    useStackStore.getState().setSelectedStack('stack-123')
    useStackStore.getState().setSelectedStack(null)
    expect(useStackStore.getState().selectedStackId).toBeNull()
  })
})

describe('stackStore setActiveTab', () => {
  it('sets active tab', () => {
    useStackStore.getState().setActiveTab('logs')
    expect(useStackStore.getState().activeTab).toBe('logs')
  })
})

describe('stackStore reset', () => {
  it('resets to initial state', () => {
    useStackStore.getState().setSelectedStack('stack-123')
    useStackStore.getState().setActiveTab('terminal')
    useStackStore.getState().reset()
    expect(useStackStore.getState().selectedStackId).toBeNull()
    expect(useStackStore.getState().activeTab).toBe('overview')
  })
})
