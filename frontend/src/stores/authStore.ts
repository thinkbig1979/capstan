import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { User } from '@/types'

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

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
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
          console.error('Logout error:', error)
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

        try {
          const { authApi } = await import('@/lib/api')
          const user = await authApi.me()
          set({
            token,
            user,
            isAuthenticated: true,
          })
        } catch (error) {
          console.error('Auth check failed:', error)
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
    }),
    {
      name: 'auth-storage',
      partialize: (state) => ({
        token: state.token,
        user: state.user,
        isAuthenticated: state.isAuthenticated,
      }),
    },
  ),
)
