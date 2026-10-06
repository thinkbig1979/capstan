package handlers

import (
	"embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// These tests are agent-os-n4ca.3. POST /auth/verify-password and
// PUT /auth/password both check the current password, but sat behind only the
// general API budget (300/min), so a stolen session cookie could brute-force
// the password far faster than the login form allows. The limiter is attached
// in the routes' own registration functions, so the tests register routes
// through those functions rather than wiring the handler directly: a test that
// wires the handler by hand cannot see the limiter go missing.

// passwordCheckTestUserHeader lets one router serve several users, standing in
// for AuthMiddleware publishing a different userID per session.
const passwordCheckTestUserHeader = "X-Test-User"

func newPasswordCheckRouter(t *testing.T, db *database.DB) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	middleware.InitRateLimiters(config.DefaultAPIRateLimitPerMin)

	const secret = "test-secret-key-32-chars-long!!!"
	router := gin.New()
	protected := router.Group("")
	protected.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader(passwordCheckTestUserHeader))
		c.Next()
	})
	NewAuthHandler(db, secret, false).RegisterProtectedRoutes(protected)
	NewSettingsHandler(db, "", secret, false, nil, nil).RegisterRoutes(protected)
	return router
}

type passwordCheckEndpoint struct {
	method, path, wrongBody string
}

var (
	verifyPasswordEndpoint = passwordCheckEndpoint{
		http.MethodPost, "/auth/verify-password", `{"password":"WrongPassword1!"}`,
	}
	changePasswordEndpoint = passwordCheckEndpoint{
		http.MethodPut, "/auth/password", `{"currentPassword":"WrongPassword1!","newPassword":"NewPassword123!"}`,
	}
)

func passwordCheckAttempt(r *gin.Engine, ep passwordCheckEndpoint, userID, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.wrongBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(passwordCheckTestUserHeader, userID)
	req.RemoteAddr = ip + ":54321"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The login budget for one account from one address is 5; the password-check
// routes use the same number, so a stolen cookie buys no more guesses than the
// login form. Spelled out here rather than imported: it is unexported, and a
// test that read it from the code under test would follow a loosened budget.
const passwordCheckPerUserIPBudget = 5

func TestPasswordCheckRoutes_AreRateLimitedPerUser(t *testing.T) {
	for _, ep := range []passwordCheckEndpoint{verifyPasswordEndpoint, changePasswordEndpoint} {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			db, err := database.NewWithMigrations(":memory:")
			require.NoError(t, err)
			defer db.Close()
			victim := seedNamedUser(t, db, "victim-user-id", "victim", "VictimPass123!")
			other := seedNamedUser(t, db, "other-user-id", "other", "OtherPass123!")
			r := newPasswordCheckRouter(t, db)
			const ip = "203.0.113.40"

			for i := 1; i <= passwordCheckPerUserIPBudget; i++ {
				w := passwordCheckAttempt(r, ep, victim.ID, ip)
				require.Equalf(t, http.StatusUnauthorized, w.Code,
					"wrong-password attempt %d is within budget and must reach the handler: %s", i, w.Body.String())
			}

			w := passwordCheckAttempt(r, ep, victim.ID, ip)
			require.Equalf(t, http.StatusTooManyRequests, w.Code,
				"attempt %d is over the per-user budget and must be refused before the password is checked: %s",
				passwordCheckPerUserIPBudget+1, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"RATE_LIMITED"`)
			assert.Equal(t, "60", w.Header().Get("Retry-After"))

			// Other side of the same instrument: a different user from the same
			// address still reaches the handler, so the bucket is per user, not
			// per IP.
			w = passwordCheckAttempt(r, ep, other.ID, ip)
			assert.Equalf(t, http.StatusUnauthorized, w.Code,
				"a different user on the same IP must not share the victim's bucket: %s", w.Body.String())
		})
	}
}

// The two routes share one budget: alternating between them must not double an
// attacker's guesses.
func TestPasswordCheckRoutes_ShareOneBudget(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()
	victim := seedNamedUser(t, db, "victim-user-id", "victim", "VictimPass123!")
	r := newPasswordCheckRouter(t, db)
	const ip = "203.0.113.41"

	for i := 1; i <= passwordCheckPerUserIPBudget; i++ {
		ep := verifyPasswordEndpoint
		if i%2 == 0 {
			ep = changePasswordEndpoint
		}
		w := passwordCheckAttempt(r, ep, victim.ID, ip)
		require.Equalf(t, http.StatusUnauthorized, w.Code, "attempt %d (%s): %s", i, ep.path, w.Body.String())
	}

	for _, ep := range []passwordCheckEndpoint{verifyPasswordEndpoint, changePasswordEndpoint} {
		w := passwordCheckAttempt(r, ep, victim.ID, ip)
		assert.Equalf(t, http.StatusTooManyRequests, w.Code,
			"%s after %d mixed attempts: %s", ep.path, passwordCheckPerUserIPBudget, w.Body.String())
	}
}

//go:embed *.go
var handlerSources embed.FS

// passwordComparingMethods returns "Receiver.Method" for every non-test method
// in this package whose body calls bcrypt.CompareHashAndPassword. Read from the
// embedded sources, so a `go test -overlay` mutant is what it sees.
func passwordComparingMethods(t *testing.T) []string {
	t.Helper()
	entries, err := handlerSources.ReadDir(".")
	require.NoError(t, err)

	var found []string
	fset := token.NewFileSet()
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := handlerSources.ReadFile(e.Name())
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, e.Name(), src, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			compares := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "CompareHashAndPassword" {
					compares = true
				}
				return !compares
			})
			if !compares {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			ident, ok := recv.(*ast.Ident)
			require.Truef(t, ok, "%s: unexpected receiver shape", fset.Position(fn.Pos()))
			found = append(found, ident.Name+"."+fn.Name.Name)
		}
	}
	sort.Strings(found)
	return found
}

// TestEveryPasswordCheckingRouteCarriesALimiter is the guard for agent-os-n4ca.3's
// class: a route whose handler checks a password, registered without a
// password-attempt limiter. It finds every handler method that calls
// bcrypt.CompareHashAndPassword, registers the package's routes through their
// real registration functions, and reads each such route's handler chain.
//
// Login is the one method whose limiter is attached outside this package:
// main.go puts middleware.RateLimitAuth on the whole /auth group before
// AuthHandler.RegisterRoutes runs, and cmd/server's
// TestMain_LoginRoutesSitBehindRateLimitAuth pins that.
func TestEveryPasswordCheckingRouteCarriesALimiter(t *testing.T) {
	const limiter = "RateLimitPasswordCheck"
	limitedElsewhere := map[string]string{
		"AuthHandler.Login": "RateLimitAuth on the /auth group in main.go",
	}

	methods := passwordComparingMethods(t)
	// Positive side of the scan: it must find the methods this package is known
	// to have, or a broken scan would pass vacuously.
	require.Contains(t, methods, "AuthHandler.Login")
	require.Contains(t, methods, "AuthHandler.VerifyPassword")
	require.Contains(t, methods, "SettingsHandler.ChangePassword")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	// The probe runs first on every route, records the chain, and stops before
	// any handler, limiter included, does work.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	chains := map[string][]string{}
	router.Use(func(c *gin.Context) {
		chains[c.Request.Method+" "+c.FullPath()] = c.HandlerNames()
		c.AbortWithStatus(http.StatusNoContent)
	})
	const secret = "test-secret-key-32-chars-long!!!"
	auth := NewAuthHandler(db, secret, false)
	auth.RegisterPublicRoutes(router.Group("/auth"))
	auth.RegisterRoutes(router.Group("/auth"))
	auth.RegisterProtectedRoutes(router.Group(""))
	NewSettingsHandler(db, "", secret, false, nil, nil).RegisterRoutes(router.Group(""))

	routesFor := map[string][]gin.RouteInfo{}
	for _, ri := range router.Routes() {
		for _, m := range methods {
			if strings.HasSuffix(ri.Handler, "."+m[strings.Index(m, ".")+1:]+"-fm") &&
				strings.Contains(ri.Handler, "(*"+m[:strings.Index(m, ".")]+")") {
				routesFor[m] = append(routesFor[m], ri)
			}
		}
	}

	for _, m := range methods {
		if why, ok := limitedElsewhere[m]; ok {
			t.Logf("%s: limited by %s", m, why)
			continue
		}
		routes := routesFor[m]
		if !assert.NotEmptyf(t, routes, "%s checks a password but no registration function in this "+
			"guard registers it; add its registration here", m) {
			continue
		}
		for _, ri := range routes {
			req := httptest.NewRequest(ri.Method, ri.Path, nil)
			router.ServeHTTP(httptest.NewRecorder(), req)
			chain := chains[ri.Method+" "+ri.Path]
			require.NotEmptyf(t, chain, "%s %s: probe did not run", ri.Method, ri.Path)
			assert.Truef(t, chainHas(chain, "middleware."+limiter+"."),
				"%s %s (%s) checks a password without middleware.%s in its chain; attach it where "+
					"the route is registered (agent-os-n4ca.3): %s",
				ri.Method, ri.Path, m, limiter, fmt.Sprint(chain))
		}
	}
}

func chainHas(chain []string, fragment string) bool {
	for _, name := range chain {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}
