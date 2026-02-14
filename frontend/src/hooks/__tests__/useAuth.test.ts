import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient } from '@tanstack/react-query'
import { useAuth } from '../useAuth'
import { renderHook } from '@testing-library/react'

vi.mock('@/stores/authStore')

describe('useAuth', () => {
  it('returns auth state and methods', () => {
    const { result } = renderHook(() => useAuth())

    expect(result.current).toHaveProperty('isAuthenticated')
    expect(result.current).toHaveProperty('authDisabled')
    expect(result.current).toHaveProperty('needsSetup')
    expect(result.current).toHaveProperty('user')
    expect(result.current).toHaveProperty('canAccess')
    expect(result.current).toHaveProperty('login')
    expect(result.current).toHaveProperty('logout')
    expect(result.current).toHaveProperty('setup')
    expect(result.current).toHaveProperty('checkAuth')
    expect(result.current).toHaveProperty('checkStatus')
  })

  it('calculates canAccess correctly when authenticated', () => {
    const { result } = renderHook(() => useAuth())
    
    if (result.current.isAuthenticated) {
      expect(result.current.canAccess).toBe(true)
    } else if (result.current.authDisabled) {
      expect(result.current.canAccess).toBe(true)
    }
  })
})
