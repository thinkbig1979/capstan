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
    // agent-os-2cp3. Same shape as checkStatus above (agent-os-a4eh): this is
    // the only production call site (App.tsx's mount effect), its deps are
    // stable Zustand actions so the effect never re-runs, and nothing else
    // re-probes -- so one transient /auth/me failure permanently logged out a
    // user whose cookie session was still perfectly valid, until they
    // reloaded by hand.
    //
    // Bounded, and only on failure: a first-attempt success returns
    // immediately, so the happy path still issues exactly ONE request. On
    // exhaustion the catch below runs unchanged, so agent-os-6hux's
    // fail-closed defaults are exactly as they were.
    const retryDelaysMs = [250, 750]

    for (let attempt = 0; ; attempt++) {
      try {
        const { authApi } = await import('@/lib/api')
        const user = await authApi.me()
        set({
          token: 'cookie',
          user,
          isAuthenticated: true,
        })
        return
      } catch (error) {
        if (attempt < retryDelaysMs.length) {
          await new Promise((resolve) => setTimeout(resolve, retryDelaysMs[attempt]))
          continue
        }
        const isDev = import.meta.env.DEV
        if (isDev) {
          console.error('Auth check failed:', error)
        }
        set({ token: null, user: null, isAuthenticated: false })
        return
      }
    }
  },

  checkStatus: async () => {
    // agent-os-a4eh. A boot probe that failed once used to be final. This is the
    // only production call site (App.tsx's mount effect), its deps are stable
    // Zustand actions so the effect never re-runs, and nothing else re-probes --
    // so one transient failure left a fresh install unable to reach /setup (the
    // sole path="/setup" sits behind needsSetup, and path="*" redirects to
    // /login) and an AUTH_DISABLED deployment showing a login form, until the
    // user reloaded by hand.
    //
    // Bounded, and only on failure: a first-attempt success returns immediately,
    // so the happy path still issues exactly ONE request. That property is what
    // makes the retry meaningful rather than just noisy, and it is pinned by a
    // test -- a fix that probed twice unconditionally would satisfy the recovery
    // arms and fail that one.
    //
    // On exhaustion the catch below runs unchanged, so agent-os-6hux's
    // fail-closed defaults are exactly as they were: retrying changes how many
    // chances a flaky network gets, never what an unreadable probe is allowed to
    // conclude.
    const retryDelaysMs = [250, 750]

    for (let attempt = 0; ; attempt++) {
      try {
        const { authApi } = await import('@/lib/api')
        const status = await authApi.status()
        set({
          authDisabled: status.authDisabled,
          needsSetup: status.needsSetup,
        })
        return
      } catch (error) {
        if (attempt < retryDelaysMs.length) {
          await new Promise((resolve) => setTimeout(resolve, retryDelaysMs[attempt]))
          continue
        }
        // Deliberately NOT dev-gated, unlike :50 and :72 in this file. Those two
        // report a failed login and a failed logout: the user initiated the
        // action, has UI feedback, and knows what they attempted. This one
        // reports a failure the user never initiated and is told nothing about --
        // the app simply looks logged out. Before this catch existed, a failed
        // boot probe left exactly one trace in every build, the unhandled
        // rejection; swallowing it in production and logging only in dev would
        // make a shipped instance less diagnosable than it was before the fix.
        console.error('Auth status check failed:', error)
        // An unreadable probe is not evidence of anything, and the two fields it
        // would have set need that principle pointed in OPPOSITE directions.
        //
        // authDisabled is forced back to the restrictive value, because it is the
        // only field that grants access on its own: useAuth derives
        // `canAccess = authDisabled || isAuthenticated`, so a true here opens the
        // whole app with no session behind it. A failed network call must never
        // be able to write that. The costs are not symmetric: true costs an
        // unauthenticated shell, while false costs a login prompt that stands
        // until the user reloads by hand. Reaching here means the bounded retry
        // above is already spent, so this value is final for the life of the
        // page: nothing probes again (see the boot effect in App.tsx).
        //
        // needsSetup is deliberately NOT written. It cannot grant access -- it
        // only routes to /setup (App.tsx:149 is the sole `path="/setup"`) -- so
        // there is nothing to defend against, and overwriting it would destroy a
        // fact this failed probe did not re-learn. A stale true at worst shows a
        // form the backend refuses: POST /auth/setup 409s SETUP_ALREADY_DONE once
        // any user exists, at the fast path (handlers/auth.go:143) and again
        // atomically inside database.CreateFirstUser, so the client value cannot
        // create an account no matter what it says.
        set({ authDisabled: false })
        return
      }
    }
  },
}))
