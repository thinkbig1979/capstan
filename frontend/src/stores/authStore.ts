import { create } from 'zustand'
import type { User } from '@/types'

const isTokenExpired = (token: string): boolean => {
  try {
    const payload = token.split('.')[1]
    const decoded = JSON.parse(atob(payload))
    const exp = decoded.exp * 1000
    return Date.now() >= exp
  } catch {
    return true
  }
}

const getStoredToken = (): string | null => {
  const token = sessionStorage.getItem('token')
  if (token && !isTokenExpired(token)) {
    return token
  }
  if (token) {
    sessionStorage.removeItem('token')
  }
  return null
}

interface AuthState {
  token: string | null
  user: User | null
  isAuthenticated: boolean
  authDisabled: boolean
  needsSetup: boolean
  login: (username: string, password: string) => Promise<void>
  setup: (username: string, password: string) => Promise<void>
  logout: () => void
  checkAuth: () => Promise<void>
  checkStatus: () => Promise<void>
}

export const useAuthStore = create<AuthState>()((set) => ({
  token: getStoredToken(),
  user: null,
  isAuthenticated: !!getStoredToken(),
  authDisabled: false,
  needsSetup: false,

  login: async (username: string, password: string) => {
    const { authApi } = await import('@/lib/api')
    const response = await authApi.login(username, password)
    set({
      token: response.token,
      user: response.user,
      isAuthenticated: true,
    })
    sessionStorage.setItem('token', response.token)
  },

  setup: async (username: string, password: string) => {
    const { authApi } = await import('@/lib/api')
    const response = await authApi.setup(username, password)
    set({
      token: response.token,
      user: response.user,
      isAuthenticated: true,
      needsSetup: false,
    })
    sessionStorage.setItem('token', response.token)
  },

  logout: async () => {
    const { authApi } = await import('@/lib/api')
    try {
      await authApi.logout()
    } catch (error) {
      const isDev = import.meta.env.DEV
      if (isDev) {
        console.error('Logout error:', error)
      }
    }
    set({
      token: null,
      user: null,
      isAuthenticated: false,
    })
    sessionStorage.removeItem('token')
  },

  checkAuth: async () => {
    const token = sessionStorage.getItem('token')
    if (!token) {
      set({ token: null, user: null, isAuthenticated: false })
      return
    }

    if (isTokenExpired(token)) {
      sessionStorage.removeItem('token')
      set({ token: null, user: null, isAuthenticated: false })
      return
    }

    try {
      const { authApi } = await import('@/lib/api')
      const user = await authApi.me()
      set({
        token,
        user,
        isAuthenticated: true,
      })
    } catch (error) {
      const isDev = import.meta.env.DEV
      if (isDev) {
        console.error('Auth check failed:', error)
      }
      sessionStorage.removeItem('token')
      set({ token: null, user: null, isAuthenticated: false })
    }
  },

  checkStatus: async () => {
    const { authApi } = await import('@/lib/api')
    const status = await authApi.status()
    set({
      authDisabled: status.authDisabled,
      needsSetup: status.needsSetup,
    })
  },
}))
