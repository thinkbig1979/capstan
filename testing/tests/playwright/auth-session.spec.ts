/**
 * Auth Session E2E — the revoke-to-logout loop (agent-os-lyx)
 *
 * WHY THIS SPEC EXISTS: no CI job has ever booted the backend with
 * AUTH_DISABLED=false, so authenticated behaviour — real sessions travelling
 * over the real wire through the real middleware chain (CSRF + rate-limit +
 * auth, in main.go's registration order) to the real frontend — has never
 * been under test. Backend unit coverage of rejection behaviour is thorough
 * (middleware/auth_test.go, handlers/ws_test.go) and is NOT repeated here.
 * What this spec covers instead is the loop: a session gets revoked, and the
 * frontend actually logs the user out because of it.
 * frontend/src/lib/error-handler.ts:45 and frontend/src/lib/api.ts:88-107
 * implement that logout — until this spec, their only tests fed a
 * hand-built axios error object; no test had ever seen the backend's real
 * 401 body reach that interceptor.
 *
 * Prerequisites (see .github/workflows/e2e-backup.yml's new auth-session
 * job for how CI provides these):
 *   - Backend booted with AUTH_DISABLED=false, JWT_SECRET >= 32 chars, a
 *     FRESH DATA_DIR (needsSetup must be true — this spec performs the
 *     first-run setup itself and only ever creates ONE account).
 *   - Frontend dev server on CAPSTAN_BASE_URL (default http://localhost:3001).
 *   - Backend API on CAPSTAN_API_URL (default http://localhost:5001).
 *
 * Run:
 *   npx playwright test testing/tests/playwright/auth-session.spec.ts
 *   # or via playwright.config.ts:
 *   npx playwright test --grep auth-session
 *
 * AUTH-BUCKET BUDGET (backend/internal/middleware/ratelimit.go:336 — 5
 * requests/min per (IP, account), shared by /auth/setup and /auth/login,
 * backend/cmd/server/main.go:327-330): this spec calls POST /auth/setup
 * EXACTLY ONCE for the whole file and never calls /auth/login. Every other
 * assertion reuses the token that single setup call returns. /auth/logout
 * is registered under the separate `protected` group (main.go:348-352),
 * which never gets RateLimitAuth, so logging out costs nothing against the
 * budget no matter how many times this spec does it.
 *
 * Serial + single shared browser context: tests share one server-side
 * session by design (there is only ever one account — no user-management
 * routes exist), and AUTH-PW-SESSION-005 specifically needs the SAME
 * browser-held cookie that AUTH-PW-SESSION-001's setup minted, still
 * present when AUTH-PW-SESSION-003 revokes it out from underneath it later.
 * Playwright's default per-test `page` fixture gets a fresh context each
 * test, which would lose that cookie — so this file creates its own
 * BrowserContext/Page in beforeAll and every UI-driving test uses that
 * instead of the injected `page` fixture.
 */

import { test, expect, type Page, type BrowserContext } from 'playwright/test'

// ─── Config ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.CAPSTAN_BASE_URL ?? 'http://localhost:3001'
const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'

// Deliberately distinct env var names from backup-flow.spec.ts's
// CAPSTAN_TEST_USER/CAPSTAN_TEST_PASSWORD: that spec's default TEST_USER is
// an email ('testadmin@example.com'), which the backend rejects with 400
// VALIDATION_ERROR (middleware/validation.go's username rule allows only
// letters, numbers, underscores, hyphens — see handlers/auth.go:449-451).
// A plain username avoids that entirely and keeps the two specs' env
// surfaces from colliding if either ever gets a shared default.
const TEST_USER = process.env.CAPSTAN_AUTH_TEST_USER ?? 'testadmin'
const TEST_PASSWORD = process.env.CAPSTAN_AUTH_TEST_PASSWORD ?? 'CapstanE2eAuth1!'

// Shared across every test in the describe block — there is only one
// account and one session for the whole file (see file header).
let bearerToken = ''
let csrfToken = ''
let sharedContext: BrowserContext
let sharedPage: Page

function authHeaders(): Record<string, string> {
  return { Authorization: `Bearer ${bearerToken}` }
}

// ─── Suite ───────────────────────────────────────────────────────────────────

test.describe('Auth session E2E', () => {
  // Serial: tests share the one account/session in this backend instance.
  // retries: 0 overrides playwright.config.ts's `retries: process.env.CI ? 1
  // : 0` for this file specifically — a CI retry would re-run the setup
  // test against a backend that now has needsSetup:false (400) and would
  // double the auth-bucket spend for no reason. See playwright.config.ts,
  // which is outside this spec's file partition and was left untouched.
  test.describe.configure({ mode: 'serial', retries: 0 })

  test.beforeAll(async ({ browser }) => {
    sharedContext = await browser.newContext()
    sharedPage = await sharedContext.newPage()
  })

  test.afterAll(async () => {
    await sharedContext?.close()
  })

  // ── 001: First-run bootstrap + real setup/login through the UI ─────────────

  test('AUTH-PW-SESSION-001: first-run bootstrap pins to /setup and creates the admin account', async () => {
    // On a fresh auth-enabled backend with no user yet, App.tsx:112-118 pins
    // every route to /setup — /login isn't a reachable route until a user
    // exists. Confirm that server-side state before touching the UI.
    const statusResp = await sharedPage.request.get(`${API_URL}/api/v1/auth/status`)
    expect(statusResp.status()).toBe(200)
    const status = await statusResp.json()
    expect(status.authDisabled).toBe(false)
    expect(status.needsSetup).toBe(true)

    await sharedPage.goto(BASE_URL)
    await sharedPage.waitForURL((u) => u.pathname === '/setup', { timeout: 15_000 })

    // Real labels from LoginForm.tsx:74-122 — 'Username', 'Password',
    // 'Confirm Password'. backup-flow.spec.ts's loginIfNeeded() targets
    // getByLabel(/email/i), which never matches this component; not reused.
    await sharedPage.getByLabel('Username', { exact: true }).fill(TEST_USER)
    await sharedPage.getByLabel('Password', { exact: true }).fill(TEST_PASSWORD)
    await sharedPage.getByLabel('Confirm Password', { exact: true }).fill(TEST_PASSWORD)

    // Intercept the UI's own POST /auth/setup response to capture the bearer
    // token it mints (handlers/auth.go's Setup returns {token, user}) — this
    // IS the real UI submission (one auth-bucket request), not a second,
    // separate API call. The captured token is reused by every later test.
    const [setupResponse] = await Promise.all([
      sharedPage.waitForResponse(
        (r) => r.url().includes('/api/v1/auth/setup') && r.request().method() === 'POST',
      ),
      sharedPage.getByRole('button', { name: 'Create Account' }).click(),
    ])
    expect(setupResponse.status()).toBe(200)
    const setupBody = await setupResponse.json()
    expect(setupBody.token).toBeTruthy()
    expect(setupBody.user?.username).toBe(TEST_USER)
    bearerToken = setupBody.token

    // setAuthCookies (handlers/auth.go:495-516) mints the CSRF cookie AND
    // echoes it in this response's X-Csrf-Token header — captured here so
    // AUTH-PW-SESSION-003's logout mutation doesn't need a separate priming
    // request. Playwright lower-cases response header names.
    csrfToken = setupResponse.headers()['x-csrf-token'] ?? ''
    expect(csrfToken, 'setup response must echo X-Csrf-Token').toBeTruthy()

    // AuthPage.tsx:42 navigates('/') on success; App.tsx:112 no longer pins
    // to /setup once needsSetup flips false in the store (authStore.ts:41).
    await sharedPage.waitForURL((u) => u.pathname === '/', { timeout: 15_000 })
    await expect(sharedPage.getByRole('link', { name: 'Settings', exact: true })).toBeVisible({
      timeout: 10_000,
    })
  })

  // ── 002: An authenticated request succeeds ──────────────────────────────────

  test('AUTH-PW-SESSION-002: the captured session authenticates real requests', async ({
    request,
  }) => {
    expect(bearerToken, 'bearer token must be captured by AUTH-PW-SESSION-001').toBeTruthy()

    const meResp = await request.get(`${API_URL}/api/v1/auth/me`, { headers: authHeaders() })
    expect(meResp.status()).toBe(200)
    const me = await meResp.json()
    expect(me.username).toBe(TEST_USER)

    const stacksResp = await request.get(`${API_URL}/api/v1/stacks`, { headers: authHeaders() })
    expect(stacksResp.status()).toBe(200)
  })

  // ── 003: Logout revokes server-side; replaying the old token 401s ──────────

  test('AUTH-PW-SESSION-003: logout revokes the session server-side (bearer replay -> 401 SESSION_EXPIRED)', async ({
    request,
  }) => {
    expect(bearerToken, 'bearer token must be captured by AUTH-PW-SESSION-001').toBeTruthy()
    expect(csrfToken, 'CSRF token must be captured by AUTH-PW-SESSION-001').toBeTruthy()

    // CSRF double-submit (middleware/csrf.go): /auth/logout sits in the
    // `protected` group (main.go:348-352), which IS behind CSRFMiddleware —
    // unlike /auth/setup and /auth/login, which are registered before it
    // (main.go:327-330) and need no CSRF header at all. CSRFMiddleware
    // checks a REQUEST COOKIE against the X-CSRF-Token header
    // (csrf.go:52-65), not just the header value in isolation — a request
    // fired from the standalone `request` fixture's own (empty) cookie jar
    // with only the header set 403s with CSRF_COOKIE_MISSING (OBSERVED: an
    // earlier draft of this test did exactly that). Manually setting the
    // Cookie header alongside it (OBSERVED via a scratch script against a
    // live instance: this reaches the server and satisfies the double-submit
    // check) supplies the missing half without needing any browser context
    // at all — deliberately NOT issued via `sharedPage.request`: that would
    // process this response's Set-Cookie (clearAuthCookies,
    // handlers/auth.go:351/518-535, clears both capstan_token and
    // capstan_csrf with Max-Age=-1) into `sharedPage`'s own cookie jar,
    // which would make AUTH-PW-SESSION-005 exercise "browser has no cookie
    // at all" instead of the intended "browser still holds a cookie it
    // believes is good" — the realistic case this spec exists to cover.
    // Only the explicit `Authorization: Bearer` header (not the manually-set
    // `capstan_token`-shaped cookie this request never even sends — only
    // capstan_csrf is set here) does the authenticating, per the constraint
    // that this must be a real Bearer-authenticated revocation.
    const logoutResp = await request.post(`${API_URL}/api/v1/auth/logout`, {
      headers: {
        ...authHeaders(),
        'X-CSRF-Token': csrfToken,
        Cookie: `capstan_csrf=${csrfToken}`,
      },
    })
    expect(logoutResp.status()).toBe(204)

    // Replay the pre-logout bearer token via the standalone `request`
    // fixture — a client that never held any cookie for this session at
    // all — to prove the session is dead server-side, not merely evicted
    // from one particular client. middleware/auth.go:176-181: the session
    // row is gone, so every request with this token now 401s with
    // SESSION_EXPIRED specifically (not just any 401 — assert the code).
    const meReplay = await request.get(`${API_URL}/api/v1/auth/me`, { headers: authHeaders() })
    expect(meReplay.status()).toBe(401)
    const meReplayBody = await meReplay.json()
    expect(meReplayBody.code).toBe('SESSION_EXPIRED')

    const stacksReplay = await request.get(`${API_URL}/api/v1/stacks`, {
      headers: authHeaders(),
    })
    expect(stacksReplay.status()).toBe(401)
    const stacksReplayBody = await stacksReplay.json()
    expect(stacksReplayBody.code).toBe('SESSION_EXPIRED')
  })

  // ── 004: A revoked WS handshake is refused at HTTP level, never upgrades ───

  test('AUTH-PW-SESSION-004: a WS handshake with revoked or missing credentials is refused with HTTP 401 and never upgrades', async ({
    request,
  }) => {
    // WS routes sit under the exact same `protected` group as REST routes
    // (main.go:435 wsGroup := protected.Group(""); main.go:475 backup WS
    // routes; logs.go:55 registers GET /ws/logs/:id the same way), so
    // AuthMiddleware runs and rejects BEFORE any upgrade attempt — a plain
    // HTTP GET (no Upgrade header) is enough to observe the rejection; the
    // gorilla websocket Upgrader in the handler is never reached, so this is
    // asserted at the HTTP-response level, never as a WS close code. A
    // revoked session never produces a 4401 close frame in the browser path:
    // ws.go's CloseCodeAuthFailure=4401 is unreachable here specifically
    // because AuthMiddleware rejects before upgradeConnection runs.
    const revokedResp = await request.get(`${API_URL}/api/v1/ws/logs/nonexistent-stack-id`, {
      headers: authHeaders(),
    })
    expect(revokedResp.status()).toBe(401)
    const revokedBody = await revokedResp.json()
    expect(revokedBody.code).toBe('SESSION_EXPIRED')

    // Browser-realistic path: a real browser's WS handshake authenticates
    // via the `capstan_token` COOKIE, not an Authorization header — App.tsx
    // registers `() => null` as the token getter, so api.ts never sets one
    // (api.ts:71-83), and extractBearerToken() (middleware/auth.go:100-108)
    // only reads the cookie once the header is absent. This constructs that
    // cookie manually with the token captured in AUTH-PW-SESSION-001 rather
    // than reading it from `sharedPage`'s own jar, because
    // AUTH-PW-SESSION-003's logout call cleared it there already
    // (clearAuthCookies — see the comment on that test); a manually-built
    // Cookie header reproduces the credential a browser tab would still
    // present if it simply hadn't processed that clearing response yet
    // (multiple tabs, a stale cached page) — same revoked session, same
    // expected rejection, no Authorization header involved at all.
    const revokedCookieResp = await request.get(
      `${API_URL}/api/v1/ws/logs/nonexistent-stack-id`,
      { headers: { Cookie: `capstan_token=${bearerToken}` } },
    )
    expect(revokedCookieResp.status()).toBe(401)
    const revokedCookieBody = await revokedCookieResp.json()
    expect(revokedCookieBody.code).toBe('SESSION_EXPIRED')

    const noCredsResp = await request.get(`${API_URL}/api/v1/ws/logs/nonexistent-stack-id`)
    expect(noCredsResp.status()).toBe(401)
    const noCredsBody = await noCredsResp.json()
    expect(noCredsBody.code).toBe('SESSION_EXPIRED')
    expect(noCredsBody.message).toMatch(/missing authorization token/i)
  })

  // ── 005: The frontend logout loop ───────────────────────────────────────────

  test('AUTH-PW-SESSION-005: a session revoked out-of-band forces the next UI action to /login', async () => {
    // sharedPage still holds the cookie session minted during
    // AUTH-PW-SESSION-001's setup — nothing has reloaded or navigated it
    // away since. AUTH-PW-SESSION-003 already revoked that same underlying
    // session server-side, but did so via an explicit
    // `Authorization: Bearer` logout issued from a SEPARATE request
    // context — deliberately out-of-band from this page.
    //
    // Revoking via the browser's own cookie (e.g. clicking a Logout button
    // in this page) would test nothing: the SPA would just be the one
    // telling itself the session is gone, and it already knows. Revoking
    // out-of-band leaves the browser holding a cookie it still believes is
    // good — so the next real request it fires is the first thing to
    // discover the session is dead, which is exactly the production
    // scenario this spec exists to cover (three P1/P2 auth bugs shipped
    // silently because nothing exercised this loop before).
    expect(new URL(sharedPage.url()).pathname).toBe('/')

    // Client-side navigation via react-router's <Link> (Header.tsx:214-218,
    // aria-label "Settings") — not page.goto/reload. A full reload would
    // re-run App.tsx's boot probe (GET /auth/me), which api.ts:104
    // deliberately exempts from the hard-redirect logout path (the auth
    // store handles a failed boot probe itself, via a different code path
    // than the one this test is verifying).
    await sharedPage.getByRole('link', { name: 'Settings', exact: true }).click()
    await sharedPage.waitForURL((u) => u.pathname === '/settings', { timeout: 15_000 })
    // Let the default 'account-security' section finish rendering before the
    // next click — mirrors backup-flow.spec.ts's expandBackupSection()
    // pattern (same click-then-settle shape) and avoids the second click
    // racing the page's own post-navigation settling (OBSERVED: an
    // instrumented run confirmed the trigger below, GET /settings/backup, is
    // the first 401 this page sees — nothing on the default section fires
    // one first).
    await sharedPage.waitForLoadState('networkidle')

    // Clicking into the Backup section mounts BackupSettingsContent, whose
    // useBackupSettings() query (hooks/useBackup.ts:11-16) fires
    // GET /settings/backup on mount — a normal authenticated request, NOT
    // /auth/me, so api.ts's isBootProbe exemption does not apply. The
    // backend 401s SESSION_EXPIRED; the interceptor (api.ts:88-107) calls
    // the logout callback registered in App.tsx:58-63, which does a hard
    // `window.location.href = '/login'`.
    await sharedPage.getByRole('link', { name: 'Backup', exact: true }).click()

    await sharedPage.waitForURL((u) => u.pathname === '/login', { timeout: 15_000 })
    expect(new URL(sharedPage.url()).pathname).toBe('/login')
  })
})
