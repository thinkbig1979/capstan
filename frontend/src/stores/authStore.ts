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

// In-flight de-duplication for checkAuth/checkStatus (agent-os-lqsa).
//
// main.tsx wraps <App/> in <StrictMode>, which double-invokes dev mount
// effects (mount -> cleanup -> remount). App.tsx's boot effect is the sole
// call site for both actions, so that double-invoke used to mean two
// independent network requests racing to `set()` the store, with no
// ordering guarantee between them -- a slow-failing first request could
// overwrite a fast-succeeding second one's result, or vice versa, well
// after the app had already rendered based on the first answer to arrive.
//
// A guard that simply dropped the "wrong" invocation's write was tried and
// rejected: checkStatus's failure path deliberately does NOT touch
// needsSetup (see its own comment below), so whichever invocation actually
// SUCCEEDS is the one carrying that fact, and a rule like "the second
// invocation always wins" can discard a genuine success in favour of the
// other invocation's failure, losing information the app needs (a fresh
// install could lose its route to /setup).
//
// De-duplication removes the race by construction instead: a call made
// while one is already in flight returns that SAME promise rather than
// issuing a second request, so both invocations always observe the
// identical outcome and there is exactly one `set()` per real probe. This
// also preserves "a first-attempt success issues exactly ONE request"
// (pinned by App.status-retry.test.tsx and App.auth-retry.test.tsx) across
// concurrent callers, not just within a single one.
let checkAuthInFlight: Promise<void> | null = null
let checkStatusInFlight: Promise<void> | null = null

// Per-attempt bound for the boot probes (agent-os-mhqf).
//
// The retry budget below and the client's request timeout used to be unrelated
// numbers. api.ts:62 sets one 120000ms timeout for the whole client, sized for
// pull/start (see its comment), and a backend that ACCEPTS the connection but
// never answers fails in exactly the shape both loops retry -- a rejection
// carrying no HTTP status. So a hang cost 120s per attempt, three attempts per
// probe, two probes in sequence behind one full-page spinner (App.tsx:105-106,
// :113): ~720s. Bounding the attempt instead of shortening the shared client
// timeout keeps long stack operations working and makes the boot budget
// ~3 x 8s + 250ms + 750ms ~= 25s per probe, ~50s for the boot.
//
// 8000ms is a JUDGEMENT, not a measurement of the thing it has to survive.
// Measured on a loopback production-serve fixture, slowest of 40 requests:
// /auth/status 0.46ms, /auth/me 0.75ms. That bounds the server's own work and
// says nothing about a slow link, so the number is chosen as ~10,000x the
// observed server time -- far above any plausible cold-start-plus-slow-network
// round trip for two liveness checks, and far below the 120s it replaces.
// These two are liveness probes, not data fetches: one that has not answered
// in eight seconds has answered.
const BOOT_PROBE_TIMEOUT_MS = 8000

/**
 * Bounds ONE probe attempt, rejecting in the shape a no-response failure
 * already has.
 *
 * The rejection deliberately carries no `status`, because that is the field
 * both loops read to tell "the server answered" from "the server never
 * answered" (api.ts:127-131, error-handler.ts:58-62). A hang IS the second
 * case, so checkAuth's status-aware predicate keeps retrying it and
 * agent-os-2cp3's narrowing is unaffected.
 *
 * The abandoned request stays in flight until axios drops it at the client
 * timeout. That is deliberate: a discarded GET against a backend that is
 * already hanging costs nothing, and the alternative -- a second timeout
 * mechanism on the same number, in api.ts -- would make this one dead code in
 * production while leaving it the only one the tests can reach.
 */
function withBootProbeTimeout<T>(work: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const bound = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => {
      reject({
        error: 'Boot probe timed out',
        code: 'ETIMEDOUT',
        message: `No response within ${BOOT_PROBE_TIMEOUT_MS}ms`,
      })
    }, BOOT_PROBE_TIMEOUT_MS)
  })
  // Once the bound wins the race nothing is listening to `work` any more, and
  // its eventual rejection would surface as an unhandled one. Claim it here.
  void work.catch(() => {})
  return Promise.race([work, bound]).finally(() => {
    clearTimeout(timer)
  })
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

  checkAuth: () => {
    // agent-os-2cp3. Same shape as checkStatus above (agent-os-a4eh): this is
    // the only production call site (App.tsx's mount effect), its deps are
    // stable Zustand actions so the effect never re-runs, and nothing else
    // re-probes -- so one transient /auth/me failure permanently logged out a
    // user whose cookie session was still perfectly valid, until they
    // reloaded by hand.
    //
    // Unlike checkStatus, retrying here must NOT fire on every rejection.
    // /auth/me is behind AuthMiddleware (api.ts:106-110), so every anonymous
    // boot -- the common case, not an edge case -- gets a genuine 401
    // SESSION_EXPIRED. A 401 is a definitive answer the server already gave;
    // retrying it cannot change the outcome, it can only cost extra requests
    // and delay the login page. Only a failure that carries no response at
    // all (network error, timeout) is worth a retry. `status` below follows
    // this codebase's own convention for telling the two apart
    // (error-handler.ts:70, classifyError): the api.ts interceptor rejects
    // with a flat object carrying `status` only when error.response existed;
    // `status === undefined` means the server never answered.
    //
    // agent-os-lqsa: de-duplicated. A call made while one is already in
    // flight (StrictMode's double-invoke, chiefly) returns that SAME
    // promise instead of issuing a second /auth/me request.
    if (checkAuthInFlight) return checkAuthInFlight

    const retryDelaysMs = [250, 750]

    checkAuthInFlight = (async () => {
      for (let attempt = 0; ; attempt++) {
        try {
          const { authApi } = await import('@/lib/api')
          const user = await withBootProbeTimeout(authApi.me())
          set({
            token: 'cookie',
            user,
            isAuthenticated: true,
          })
          return
        } catch (error) {
          const err = error as { status?: number; response?: { status?: number } }
          const status = err?.status ?? err?.response?.status
          const gotResponse = status !== undefined

          if (!gotResponse && attempt < retryDelaysMs.length) {
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
    })().finally(() => {
      checkAuthInFlight = null
    })

    return checkAuthInFlight
  },

  checkStatus: () => {
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
    //
    // agent-os-lqsa: de-duplicated, for the same reason as checkAuth above.
    // This one matters more: the failure path below deliberately leaves
    // needsSetup untouched, so a rule that discarded one invocation's result
    // outright (rather than sharing a single request) could throw away a
    // genuine needsSetup: true learned by the invocation it discarded.
    if (checkStatusInFlight) return checkStatusInFlight

    const retryDelaysMs = [250, 750]

    checkStatusInFlight = (async () => {
      for (let attempt = 0; ; attempt++) {
        try {
          const { authApi } = await import('@/lib/api')
          const status = await withBootProbeTimeout(authApi.status())
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
    })().finally(() => {
      checkStatusInFlight = null
    })

    return checkStatusInFlight
  },
}))
