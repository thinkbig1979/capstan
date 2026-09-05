package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// withHandlerDoneSignal wraps router with a middleware that signals handled
// after the ENTIRE handler chain (c.Next()) returns, and returns router so
// callers can chain it directly into RegisterRoutes.
//
// REQUIRED, not decorative: a plain *bytes.Buffer (captureHandlerLogs) has no
// internal synchronization, and the WS/HTTP handler runs on httptest.Server's
// OWN goroutine, separate from the test goroutine that reads the buffer.
// dashboard_ws_refusal_test.go documents the identical hazard and uses the
// identical fix ("the channel send gives the read a happens-before edge").
// An EARLIER version of this file polled buf.String() in a loop instead
// (matching requireConnRegistered's shape for a different problem: waiting
// for an event, not synchronizing a memory access) and OBSERVED a real
// WARNING: DATA RACE under `-race` (write in slog's TextHandler from the
// server goroutine racing the read in the poll loop from the test goroutine)
// -- not hypothetical, not a flake, reproduced on both the logs.go-only site
// and the dashboard.go site. c.Next() returning is a normal Go function
// return regardless of whether the handler hijacked the connection, so this
// is safe for WS routes too.
func withHandlerDoneSignal(router *gin.Engine) (*gin.Engine, <-chan struct{}) {
	handled := make(chan struct{}, 8)
	router.Use(func(c *gin.Context) {
		c.Next()
		select {
		case handled <- struct{}{}:
		default:
		}
	})
	return router, handled
}

// waitHandled blocks for one signal from withHandlerDoneSignal's channel, or
// fails the test at the hang-guard deadline.
func waitHandled(t *testing.T, handled <-chan struct{}) {
	t.Helper()
	select {
	case <-handled:
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("the handler never completed (no done signal) before the hang-guard deadline")
	}
}

// TestWSAuthFailure_LogsExactlyOnceAtASilentSite is the seen-failing-first
// regression for agent-os-94yx: logs.go is one of the four serveWS call
// sites (agent-os-o1jp.1) that report NOTHING when upgradeConnection fails --
// `if err != nil { return }`, no handleError, no slog call anywhere. A WS
// client that completes the handshake and then fails auth (invalid token, no
// AUTH_DISABLED bypass) left no record anywhere that it happened.
//
// Drives the auth failure via upgradeConnection's non-cookie branch: no
// capstan_token cookie is sent, so it falls into conn.ReadJSON(&authMsg), and
// the client immediately sends an invalid token -- this resolves in
// milliseconds via authenticateToken -> middleware.ValidateJWT failing to
// parse, rather than waiting out the 5s "no auth frame" timeout branch (the
// other way to reach the same AppError-401 shape).
//
// Seen failing first: with serveWS's new log line absent, `go build ./...`
// exits 0 and this test fails on its ASSERTION (captured ERROR-line count is
// 0, not the compile error `undefined: X`).
func TestWSAuthFailure_LogsExactlyOnceAtASilentSite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cm := NewConnectionManager(10)
	srv, handled := newLogsAuthEnabledFixture(t, cm)

	code, text := dialWSAndSendInvalidToken(t, srv, "/api/ws/logs/stack-a")
	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeAuthFailure) -- the auth failure itself didn't happen the way this test assumes", code, text, CloseCodeAuthFailure)
	}
	waitHandled(t, handled)

	got := buf.String()
	n := strings.Count(got, "level=ERROR")
	if n != 1 {
		t.Fatalf("a WS auth failure at logs.go (a silent call site) produced %d ERROR line(s), want exactly 1. captured = %q", n, got)
	}
	if !strings.Contains(got, "WebSocket upgrade failed") {
		t.Errorf("the ERROR line doesn't name what happened. captured = %q", got)
	}
}

// TestHandleError_NeverLogsA401AfterAWSAuthFailure is the control proving
// "handleError never logs a 401" -- combining the real, end-to-end serveWS
// log (via the SAME dial as the test above, against logs.go, a silent site)
// with a DIRECT call to handleError carrying the exact AppError shape a
// handleError call site (monitoring.go x2, dashboard.go, terminal.go) would
// receive from upgradeConnection's auth-failure branch.
//
// NOT driven end-to-end through dashboard.go on purpose: dashboard.go's own
// handleError(c, err) call, for this exact 401/AppError shape, attempts
// c.JSON on a connection upgradeConnection ALREADY hijacked (unlike the
// errWSRefused case, which is explicitly guarded against for the same
// reason, agent-os-o1jp.1 -- the 401/AppError shape has no equivalent
// guard). That is a PRE-EXISTING, independent defect (not introduced by this
// fix, and none of monitoring.go/dashboard.go/terminal.go are in my FILES),
// OBSERVED to also make net/http log "response.Write on hijacked connection"
// from its OWN internal, unsynchronized goroutine straight into the same
// captured buffer -- a genuine DATA RACE (not a flake), reproduced against
// an early draft of this test: writer stack net/http.(*Server).logf ->
// dashboard.go:161's handleError(c, err) -> gin's hijacked ResponseWriter.
// Escalated in my final report per clause 7 (STANDING RULE: a correct fix
// must not silently route around a file outside FILES:) rather than fixed
// here or silently avoided without comment.
//
// This version verifies the exact same claim (0 lines from handleError for a
// 401) without exercising that unrelated hazard: gin.CreateTestContext here
// never performs a real hijack, so there is nothing for handleError's c.JSON
// to collide with.
func TestHandleError_NeverLogsA401AfterAWSAuthFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cm := NewConnectionManager(10)
	srv, handled := newLogsAuthEnabledFixture(t, cm)

	code, text := dialWSAndSendInvalidToken(t, srv, "/api/ws/logs/stack-a")
	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeAuthFailure)", code, text, CloseCodeAuthFailure)
	}
	waitHandled(t, handled)

	before := buf.String()
	nBefore := strings.Count(before, "level=ERROR")
	if nBefore != 1 {
		t.Fatalf("serveWS's own log (the thing under test) produced %d ERROR line(s), want exactly 1 before checking handleError. captured = %q", nBefore, before)
	}

	// The exact shape upgradeConnection's auth-failure branch returns
	// (ws.go's authenticateToken, "otherwise invalid" case), fed straight to
	// handleError the way monitoring.go/dashboard.go/terminal.go do. Single
	// goroutine, no real hijack: safe to read buf immediately afterward.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, &models.AppError{Code: models.ErrSessionExpired, Message: "Invalid token", Status: http.StatusUnauthorized})

	after := buf.String()
	nAfter := strings.Count(after, "level=ERROR")
	if nAfter != nBefore {
		t.Fatalf("handleError(401 AppError) added %d ERROR line(s) (went from %d to %d) -- "+
			"logServerFault must stay silent below 500. captured = %q", nAfter-nBefore, nBefore, nAfter, after)
	}
}

// TestUpgradeFailure_SilentSiteLogsExactlyOnce is the raw-HandshakeError/5xx
// control at a silent site (logs.go): a plain, non-WebSocket GET makes
// upgrader.Upgrade itself fail (agent-os-zaor's mechanism), which is NOT a
// *models.AppError, so serveWS's new log names it "UPGRADE_FAILED". Uses a
// local fixture (not the shared newLogsCapFixture) so the same
// withHandlerDoneSignal synchronization applies here too -- a plain HTTP
// response reaching the client is not, by itself, something the race
// detector treats as synchronizing a Go-level memory access.
func TestUpgradeFailure_SilentSiteLogsExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cm := NewConnectionManager(10)
	srv, handled := newLogsAuthEnabledFixture(t, cm)

	resp, err := http.Get(srv.URL + "/api/ws/logs/stack-a")
	require.NoError(t, err)
	resp.Body.Close()
	waitHandled(t, handled)

	got := buf.String()
	n := strings.Count(got, "level=ERROR")
	if n != 1 {
		t.Fatalf("a raw upgrade failure at logs.go (a silent call site) produced %d ERROR line(s), want exactly 1. captured = %q", n, got)
	}
	if !strings.Contains(got, "not using the websocket protocol") {
		t.Errorf("the ERROR line doesn't carry the underlying cause. captured = %q", got)
	}
	if !strings.Contains(got, "UPGRADE_FAILED") {
		t.Errorf(`a raw HandshakeError (not a *models.AppError) must be coded "UPGRADE_FAILED". captured = %q`, got)
	}
}

// TestUpgradeFailure_HandleErrorSiteLogsTwice is the SAME raw-HandshakeError
// control at a handleError call site (dashboard.go): unlike the 401 shape,
// this one IS treated as a generic 500 by handleError's fallback (it is not
// a *models.AppError), so logServerFault (>=500) DOES fire there, on top of
// serveWS's own new line -- exactly 2, not 1. This is the accepted
// agent-os-ua4y double-log precedent named in the bead, demonstrated rather
// than assumed. No hijack happens on this path (upgrader.Upgrade failed
// before any hijack), so dashboard.go's handleError call is safe here,
// unlike the 401 shape above.
func TestUpgradeFailure_HandleErrorSiteLogsTwice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cm := NewConnectionManager(4)
	srv, handled := newDashboardAuthEnabledFixture(t, cm)

	resp, err := http.Get(srv.URL + "/api/ws/dashboard/metrics")
	require.NoError(t, err)
	resp.Body.Close()
	waitHandled(t, handled)

	got := buf.String()
	n := strings.Count(got, "level=ERROR")
	if n != 2 {
		t.Fatalf("a raw upgrade failure at dashboard.go (a handleError call site) produced %d ERROR line(s), want exactly 2 "+
			"(serveWS's new line + handleError's existing 500 fallback -- the accepted agent-os-ua4y double-log precedent). captured = %q", n, got)
	}
}

// newLogsAuthEnabledFixture is newLogsCapFixture's authDisabled=false twin,
// with a done-signal middleware (see withHandlerDoneSignal): the named
// fixture in ws_cap_refusal_close_code_test.go hardcodes true (never reaches
// upgradeConnection's auth-token branch) and has no synchronization signal,
// so a log-capturing test needs its own variant rather than reusing it.
func newLogsAuthEnabledFixture(t *testing.T, cm *ConnectionManager) (*httptest.Server, <-chan struct{}) {
	t.Helper()

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	handler := NewLogsHandler(docker, db, "test-secret-key-32-chars-long!!!", false, t.TempDir(), cm)

	router, handled := withHandlerDoneSignal(gin.New())
	handler.RegisterRoutes(router.Group("/api"))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, handled
}

// newDashboardAuthEnabledFixture is newDashboardCapFixture's twin with a
// done-signal middleware, for the same reason as newLogsAuthEnabledFixture
// above (authDisabled value doesn't matter for the raw-HandshakeError shape
// this fixture is used for -- the upgrade fails before either auth branch).
func newDashboardAuthEnabledFixture(t *testing.T, cm *ConnectionManager) (*httptest.Server, <-chan struct{}) {
	t.Helper()

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewDashboardHandler(nil, docker, db, cm)

	router, handled := withHandlerDoneSignal(gin.New())
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", false)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, handled
}

// dialWSAndSendInvalidToken completes a WebSocket handshake with no cookie
// (so upgradeConnection reads a JSON auth message instead), sends an
// obviously-invalid token, and returns the close code the server sends back.
// Same shape as readRefusalCloseCode (ws_cap_refusal_close_code_test.go),
// reused rather than adding a distinct dial helper, extended with the one
// extra step (WriteJSON) this bead's auth handshake needs.
func dialWSAndSendInvalidToken(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer conn.Close()
	defer resp.Body.Close()

	require.NoError(t, conn.WriteJSON(map[string]string{"type": "auth", "token": "not-a-valid-jwt"}))

	closeCode := 0
	closeText := ""
	conn.SetCloseHandler(func(code int, text string) error {
		closeCode, closeText = code, text
		return nil
	})

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ce, ok := err.(*websocket.CloseError); ok && closeCode == 0 {
				closeCode, closeText = ce.Code, ce.Text
			}
			break
		}
	}
	return closeCode, closeText
}
