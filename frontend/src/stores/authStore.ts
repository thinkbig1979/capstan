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
    try {
      const { authApi } = await import('@/lib/api')
      const status = await authApi.status()
      set({
        authDisabled: status.authDisabled,
        needsSetup: status.needsSetup,
      })
    } catch (error) {
      const isDev = import.meta.env.DEV
      if (isDev) {
        console.error('Auth status check failed:', error)
      }
      // An unreadable probe is not evidence of anything, and the two fields it
      // would have set need that principle pointed in OPPOSITE directions.
      //
      // authDisabled is forced back to the restrictive value, because it is the
      // only field that grants access on its own: useAuth derives
      // `canAccess = authDisabled || isAuthenticated`, so a true here opens the
      // whole app with no session behind it. A failed network call must never
      // be able to write that. False costs a login prompt on a transient
      // failure; true costs an unauthenticated shell. Not symmetric.
      //
      // needsSetup is deliberately NOT written. It cannot grant access -- it
      // only routes to /setup (App.tsx:144 is the sole `path="/setup"`) -- so
      // there is nothing to defend against, and overwriting it would destroy a
      // fact this failed probe did not re-learn. A stale true at worst shows a
      // form the backend refuses: POST /auth/setup 409s SETUP_ALREADY_DONE once
      // any user exists, at the fast path (handlers/auth.go:143) and again
      // atomically inside database.CreateFirstUser, so the client value cannot
      // create an account no matter what it says.
      set({ authDisabled: false })
    }
  },
}))
