import { create } from 'zustand'
import type { User } from '@/types'

interface AuthState {
  token: string | null
  user: User | null
  isAuthenticated: boolean
  authDisabled: boolean
  needsSetup: boolean
  login: (username: string, password: string) => Promise<void>
  setup: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  checkAuth: () => Promise<void>
  checkStatus: () => Promise<void>
}

export const useAuthStore = create<AuthState>()((set) => ({
  token: null,
  user: null,
  isAuthenticated: false,
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
  },

  checkAuth: async () => {
    try {
      const { authApi } = await import('@/lib/api')
      const user = await authApi.me()
      set({
        token: 'cookie',
        user,
        isAuthenticated: true,
      })
    } catch (error) {
      const isDev = import.meta.env.DEV
      if (isDev) {
        console.error('Auth check failed:', error)
      }
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
