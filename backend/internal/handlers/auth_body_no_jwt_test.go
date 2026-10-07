package handlers

import (
	"bytes"
	"embed"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-qags.11. The session credential lives only in the HttpOnly
// capstan_token cookie (setAuthCookies); a JSON body is readable by page script,
// so a JWT there defeats the HttpOnly flag. agent-os-n4ca.2 removed it from the
// Setup and Login bodies. This file keeps it out: it drives EVERY route under
// /api/v1/auth/ and scans every response body for the issued JWT and for any
// JWT-shaped value.
//
// The one deliberate body credential is `unlockToken` (mintUnlock): a short-lived
// step-up token that is not a session credential, sent back as X-Unlock-Token and
// revoked on logout. It is exempt from the SHAPE scan by key name only. It is
// never exempt from the exact-match scan, so an unlockToken that was really the
// session JWT would still fail.

// authBodySources holds the handler package's own source, embedded rather than
// read from disk so a `go test -overlay` mutant of a handler file is what the
// registrar scan below sees. (A parser.ParseFile(path, nil) would read the
// unmutated disk copy.)
//
//go:embed *.go
var authBodySources embed.FS

const (
	authBodyPrefix = "/api/v1/auth"
	// A syntactic JWT: three base64url segments joined by dots. The segment
	// floor of 10 keeps the dotted file names and versions that legitimately
	// appear in other bodies from matching; a real header/claims/signature is
	// far longer.
	authBodyJWTShape = `[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`
	authBodyUnlock   = "unlockToken"

	authBodyUsername = "bodyscanuser"
	authBodyPassword = "Str0ng!Passw0rd"
	authBodyNewPass  = "N3w!Str0ngPassw0rd"
	authBodySecret   = "test-secret-key-32-chars-or-more"
)

var authBodyJWTRe = regexp.MustCompile(authBodyJWTShape)

// authRouteKey is "METHOD /path" with the /api/v1/auth prefix removed, so the
// router view (full paths) and the registrar view (group-relative paths, and
// "/auth/..." for routes mounted on the API root) meet on one key.
func authRouteKey(method, path string) string {
	path = strings.TrimPrefix(path, authBodyPrefix)
	if strings.HasPrefix(path, "/auth/") {
		path = strings.TrimPrefix(path, "/auth")
	}
	return method + " " + path
}

// authBodyRoutes is the table of every route under /api/v1/auth/. Adding a route
// without a row here fails TestAuthBody_RouteTableCoversEveryAuthRoute; a row for
// a route that no longer exists fails it too.
var authBodyRoutes = []string{
	"GET /status",
	"POST /setup",
	"POST /login",
	"POST /logout",
	"GET /me",
	"POST /verify-password",
	"PUT /password",
}

type authBodyEnv struct {
	router *gin.Engine
	// jwts are the session JWTs the app issued during the run, read off the
	// capstan_token Set-Cookie. The exact-match scan looks for each of them.
	jwts []string
	// loginCookie authenticates the protected routes.
	loginCookie string
}

func newAuthBodyEnv(t *testing.T) *authBodyEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// RateLimitPasswordCheck is part of the chain RegisterProtectedRoutes and
	// SettingsHandler.RegisterRoutes install, and it fails closed (500) when the
	// limiters were never built. Rebuilding them here also resets the buckets,
	// so repeated -count runs never see a 429 from the previous run. This run
	// spends 4 of the 5 password-check attempts one account may make in a minute.
	middleware.InitRateLimiters(config.DefaultAPIRateLimitPerMin)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	authHandler := NewAuthHandler(db, authBodySecret, false)
	// Wired so verify-password really mints an unlockToken: the exception is
	// exercised, not just declared.
	authHandler.SetEnvUnlockStore(services.NewEnvUnlockStore())
	settingsHandler := NewSettingsHandler(db, "", authBodySecret, false, nil, nil)

	// Mirrors cmd/server/main.go's wiring of the /auth routes: the public and
	// rate-limited group, then the AuthMiddleware-protected group that carries
	// logout, me, verify-password and the settings handler's /auth/password.
	router := gin.New()
	api := router.Group("/api/v1")
	authGroup := api.Group("/auth")
	authHandler.RegisterPublicRoutes(authGroup)
	authHandler.RegisterRoutes(authGroup)
	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(db, authBodySecret, false, ""))
	authHandler.RegisterProtectedRoutes(protected)
	settingsHandler.RegisterRoutes(protected)

	return &authBodyEnv{router: router}
}

func (e *authBodyEnv) do(t *testing.T, method, path string, body any, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(mustMarshal(t, body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, authBodyPrefix+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "capstan_token", Value: cookie})
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// sessionCookie returns the capstan_token the response set.
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "capstan_token" {
			return c.Value
		}
	}
	t.Fatalf("response set no capstan_token cookie (status %d, body %q)", w.Code, w.Body.String())
	return ""
}

// scanAuthBody reports every way a response body leaks a session credential.
// label names the request so the failure points at the route.
func scanAuthBody(t *testing.T, label string, body []byte, jwts []string) {
	t.Helper()
	for _, jwt := range jwts {
		assert.NotContains(t, string(body), jwt,
			"%s: the response body carries the JWT the app just issued; the session credential belongs only in the HttpOnly capstan_token cookie (agent-os-n4ca.2)", label)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Not JSON: scan the raw bytes, with no key exemption to apply.
		assert.False(t, authBodyJWTRe.Match(body),
			"%s: a non-JSON response body contains a JWT-shaped value: %q", label, body)
		return
	}
	walkJSONForJWT(t, label, "$", parsed)
}

// walkJSONForJWT flags a JWT-shaped string anywhere in the document, exempting
// only a value stored under the key `unlockToken`. Object keys are scanned too,
// so a JWT used as a map key does not slip past.
func walkJSONForJWT(t *testing.T, label, at string, v any) {
	t.Helper()
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			assert.False(t, authBodyJWTRe.MatchString(k),
				"%s: object key at %s is JWT-shaped: %q", label, at, k)
			if k == authBodyUnlock {
				continue
			}
			walkJSONForJWT(t, label, at+"."+k, val[k])
		}
	case []any:
		for i, item := range val {
			walkJSONForJWT(t, label, at+"["+strconv.Itoa(i)+"]", item)
		}
	case string:
		assert.False(t, authBodyJWTRe.MatchString(val),
			"%s: %s is a JWT-shaped string (%q); a session credential in a body is readable by page script", label, at, val)
	}
}

// TestAuthBody_NoAuthResponseCarriesAJWT drives every /api/v1/auth route to its
// success status, plus the failure bodies that share those handlers, and scans
// each response body. The order is a session lifecycle, so it is a single test
// with steps rather than independent subtests.
func TestAuthBody_NoAuthResponseCarriesAJWT(t *testing.T) {
	env := newAuthBodyEnv(t)
	driven := map[string]bool{}

	// step runs one request, asserts its status (so a driver that quietly hit a
	// 401/400 cannot read as a clean body), scans the body, and records the route
	// as driven when it is the success path.
	step := func(method, path string, body any, cookie string, wantStatus int, covers bool) *httptest.ResponseRecorder {
		t.Helper()
		label := method + " " + authBodyPrefix + path + " -> " + strconv.Itoa(wantStatus)
		w := env.do(t, method, path, body, cookie)
		require.Equal(t, wantStatus, w.Code, "%s: unexpected status, body %q", label, w.Body.String())
		scanAuthBody(t, label, w.Body.Bytes(), env.jwts)
		if covers {
			driven[authRouteKey(method, path)] = true
		}
		return w
	}

	creds := map[string]string{"username": authBodyUsername, "password": authBodyPassword}

	step(http.MethodGet, "/status", nil, "", http.StatusOK, true)

	setup := step(http.MethodPost, "/setup", creds, "", http.StatusOK, true)
	setupJWT := sessionCookie(t, setup)
	// Positive control on the shape instrument: the JWT the app really issued
	// must match the pattern the scan uses, or a clean scan proves nothing.
	require.Regexp(t, authBodyJWTRe, setupJWT, "the JWT setup issued must match the shape the scan looks for")
	env.jwts = append(env.jwts, setupJWT)
	// Re-scan the setup body now that its JWT is known: the exact-match scan
	// inside step ran before the JWT was read off the cookie.
	scanAuthBody(t, "POST /setup (exact-match pass)", setup.Body.Bytes(), env.jwts)

	// Failure bodies from the same handlers.
	step(http.MethodPost, "/setup", creds, "", http.StatusConflict, false)
	step(http.MethodPost, "/login", map[string]string{"username": authBodyUsername, "password": "wrong-Passw0rd!"}, "", http.StatusUnauthorized, false)

	login := step(http.MethodPost, "/login", creds, "", http.StatusOK, true)
	loginJWT := sessionCookie(t, login)
	require.Regexp(t, authBodyJWTRe, loginJWT)
	require.NotEqual(t, setupJWT, loginJWT, "login must issue its own session, not echo setup's")
	env.jwts = append(env.jwts, loginJWT)
	scanAuthBody(t, "POST /login (exact-match pass)", login.Body.Bytes(), env.jwts)
	env.loginCookie = loginJWT

	step(http.MethodGet, "/me", nil, env.loginCookie, http.StatusOK, true)

	verify := step(http.MethodPost, "/verify-password", map[string]string{"password": authBodyPassword}, env.loginCookie, http.StatusOK, true)
	var verified map[string]any
	require.NoError(t, json.Unmarshal(verify.Body.Bytes(), &verified))
	unlock, _ := verified[authBodyUnlock].(string)
	require.NotEmpty(t, unlock, "verify-password must mint an unlockToken, or the named exception is not being exercised")
	assert.NotContains(t, env.jwts, unlock, "the unlockToken must never be a session JWT")
	assert.NotRegexp(t, authBodyJWTRe, unlock, "the unlockToken is an opaque token, not a JWT-shaped value")
	step(http.MethodPost, "/verify-password", map[string]string{"password": "wrong-Passw0rd!"}, env.loginCookie, http.StatusUnauthorized, false)

	step(http.MethodPut, "/password", map[string]string{"currentPassword": "wrong-Passw0rd!", "newPassword": authBodyNewPass}, env.loginCookie, http.StatusUnauthorized, false)
	step(http.MethodPut, "/password", map[string]string{"currentPassword": authBodyPassword, "newPassword": authBodyNewPass}, env.loginCookie, http.StatusNoContent, true)

	// Last: logout revokes the session the cookie names.
	step(http.MethodPost, "/logout", nil, env.loginCookie, http.StatusNoContent, true)

	var undriven []string
	for _, route := range authBodyRoutes {
		if !driven[route] {
			undriven = append(undriven, route)
		}
	}
	assert.Empty(t, undriven, "routes in the table that this test never drove to their success status")
}

// authRoutesFromRouter is the router's own list of /api/v1/auth routes, as keys.
func authRoutesFromRouter(t *testing.T) []string {
	t.Helper()
	env := newAuthBodyEnv(t)
	var keys []string
	for _, r := range env.router.Routes() {
		if strings.HasPrefix(r.Path, authBodyPrefix+"/") {
			keys = append(keys, authRouteKey(r.Method, r.Path))
		}
	}
	sort.Strings(keys)
	return keys
}

var authBodyVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
	"HEAD": true, "OPTIONS": true, "Any": true, "Handle": true,
}

// authRoutesFromSource lists the /auth routes the handler package's own source
// registers, keyed on the REGISTRAR rather than on a path literal: group-relative
// paths such as "/setup" carry no "/auth" text. It collects
//   - every route-verb call (GET, POST, ..., Handle) inside a method whose
//     receiver is *AuthHandler, whatever its name, and
//   - every route-verb call anywhere in the package whose path literal starts
//     "/auth/" (SettingsHandler's PUT /auth/password), and
//   - every other string literal starting "/auth/" as a path-only entry, keyed
//     with method "?", so a path routed some way this scan does not model still
//     has to appear in the table.
func authRoutesFromSource(t *testing.T) []string {
	t.Helper()
	entries, err := authBodySources.ReadDir(".")
	require.NoError(t, err)

	found := map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := authBodySources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err, name)

		// Each registration call is attributed to its enclosing func, so the
		// receiver test below is per-call rather than per-file.
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			onAuthHandler := receiverIsAuthHandler(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					method, path, ok := registrationCall(n)
					if !ok {
						return true
					}
					if onAuthHandler || strings.HasPrefix(path, "/auth/") {
						found[authRouteKey(method, path)] = true
					}
				case *ast.BasicLit:
					if n.Kind != token.STRING {
						return true
					}
					if lit, err := strconv.Unquote(n.Value); err == nil && strings.HasPrefix(lit, "/auth/") {
						found[authRouteKey("?", lit)] = true
					}
				}
				return true
			})
		}
	}

	// A "?" entry is a bare path literal; it is satisfied when some table row has
	// that path, and a registration with a known method already produced its own
	// keyed entry, so drop the "?" twin of every one of those.
	var keys []string
	for k := range found {
		if strings.HasPrefix(k, "? ") {
			path := strings.TrimPrefix(k, "? ")
			twinned := false
			for _, row := range authBodyRoutes {
				if strings.HasSuffix(row, " "+path) && found[row] {
					twinned = true
					break
				}
			}
			if twinned {
				continue
			}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func receiverIsAuthHandler(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.Name == "AuthHandler"
}

// registrationCall recognises x.GET("/path", ...) and x.Handle("METHOD", "/path", ...).
func registrationCall(call *ast.CallExpr) (method, path string, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel || !authBodyVerbs[sel.Sel.Name] {
		return "", "", false
	}
	lits := func(i int) (string, bool) {
		if i >= len(call.Args) {
			return "", false
		}
		bl, isLit := call.Args[i].(*ast.BasicLit)
		if !isLit || bl.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(bl.Value)
		return s, err == nil
	}
	if sel.Sel.Name == "Handle" {
		m, okM := lits(0)
		p, okP := lits(1)
		return m, p, okM && okP
	}
	p, okP := lits(0)
	return strings.ToUpper(sel.Sel.Name), p, okP
}

// TestAuthBody_RouteTableCoversEveryAuthRoute is the "a new route is covered"
// half: the router's /api/v1/auth routes AND the registrar scan of the handler
// source must each equal the table. A new route fails here until it has a row
// in authBodyRoutes and a step in TestAuthBody_NoAuthResponseCarriesAJWT; a row
// for a removed route fails here too, so the table cannot rot.
func TestAuthBody_RouteTableCoversEveryAuthRoute(t *testing.T) {
	want := append([]string(nil), authBodyRoutes...)
	sort.Strings(want)

	fromRouter := authRoutesFromRouter(t)
	assert.Equal(t, want, fromRouter,
		"the router's /api/v1/auth routes differ from authBodyRoutes; add the new route to the table and drive it in TestAuthBody_NoAuthResponseCarriesAJWT")

	fromSource := authRoutesFromSource(t)
	t.Logf("registrar scan of the embedded handler source found %d auth routes: %v", len(fromSource), fromSource)
	assert.Equal(t, want, fromSource,
		"the handler source registers an /auth route the table does not list (or the table lists one the source no longer registers)")
}
