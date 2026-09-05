package handlers

import (
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
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

// countErrorLines counts the captured lines that are BOTH level=ERROR and
// carry mustContain (agent-os-737f). captureHandlerLogs's buffer is slog's
// PROCESS-GLOBAL default sink for as long as the test runs, so a bare
// count of every level=ERROR token in it is perturbed by any goroutine
// that logs ERROR in the window (a leak from an earlier test, net/http
// teardown): a logical race the race detector can never report. Every
// count in this file therefore discriminates on the test's own request_id
// sentinel, which the fixtures' RequestID middleware puts on serveWS's line
// (ws.go, "request_id", middleware.RequestIDFrom(c)) and logServerFault's
// (respond.go), and which no other goroutine can carry.
func countErrorLines(captured, mustContain string) int {
	n := 0
	for _, line := range strings.Split(captured, "\n") {
		if strings.Contains(line, "level=ERROR") && strings.Contains(line, mustContain) {
			n++
		}
	}
	return n
}

// requestIDSentinel returns a fresh per-test request ID and the attr text
// serveWS/logServerFault will emit for it. A well-formed UUID, because
// middleware.RequestID only honours an inbound X-Request-ID that parses as
// one (requestid.go); anything else is replaced with a random ID and the
// discriminator would never match.
func requestIDSentinel() (id, attr string) {
	id = uuid.NewString()
	return id, "request_id=" + id
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

	// PLANT (agent-os-737f): a stray ERROR line from a goroutine this test
	// did not synchronise with, logged through slog.Default() inside the
	// capture window -- the buffer is the PROCESS-GLOBAL sink for the
	// duration of the test, so a goroutine leaked by an earlier test, or
	// net/http teardown, can land a line here. Same msg, same code, a
	// DIFFERENT request_id: only a count that discriminates on this test's
	// own request_id stays at 1. Seen failing first: with the bare
	// count of every level=ERROR token in the buffer this reads 2.
	plantDone := plantStrayUpgradeFailureLine(t)

	reqID, sentinel := requestIDSentinel()
	code, text := dialWSAndSendInvalidToken(t, srv, "/api/ws/logs/stack-a", reqID)
	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeAuthFailure) -- the auth failure itself didn't happen the way this test assumes", code, text, CloseCodeAuthFailure)
	}
	waitHandled(t, handled)
	<-plantDone

	got := buf.String()
	// The class this pin guards against, stated on the same buffer: the OLD
	// undiscriminated count (every level=ERROR token in the shared sink)
	// reads 2 here, because the plant's line counts the same as serveWS's.
	// If this ever reads 1 the plant did not land in the window and the
	// discriminated assertion below is no longer proving anything.
	if bare := strings.Count(got, "level=ERROR"); bare != 2 {
		t.Fatalf("the undiscriminated count over the shared sink read %d, want 2 (serveWS's line + the planted stray line): "+
			"that over-count is the class agent-os-737f pins; the plant is not in the window, so the discriminated count below proves nothing. captured = %q", bare, got)
	}
	n := countErrorLines(got, sentinel)
	if n != 1 {
		t.Fatalf("a WS auth failure at logs.go (a silent call site) produced %d ERROR line(s) carrying %s, want exactly 1. captured = %q", n, sentinel, got)
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
// here or silently avoided without comment. Since fixed as agent-os-lukw
// (the four post-serveWS handleError calls are gone); the end-to-end 401
// drive through dashboard.go now lives in
// TestWSAuthFailure_HandleErrorSiteNeverWritesToTheHijackedConnection.
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

	reqID, sentinel := requestIDSentinel()
	code, text := dialWSAndSendInvalidToken(t, srv, "/api/ws/logs/stack-a", reqID)
	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeAuthFailure)", code, text, CloseCodeAuthFailure)
	}
	waitHandled(t, handled)

	before := buf.String()
	nBefore := countErrorLines(before, sentinel)
	if nBefore != 1 {
		t.Fatalf("serveWS's own log (the thing under test) produced %d ERROR line(s) carrying %s, want exactly 1 before checking handleError. captured = %q", nBefore, sentinel, before)
	}

	// The exact shape upgradeConnection's auth-failure branch returns
	// (ws.go's authenticateToken, "otherwise invalid" case), fed straight to
	// handleError the way monitoring.go/dashboard.go/terminal.go do. Single
	// goroutine, no real hijack: safe to read buf immediately afterward.
	// The SAME request ID is set on this context so that a logServerFault
	// line, if it ever fired, would carry the sentinel and be counted below
	// -- the discriminated delta keeps its teeth (agent-os-737f).
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.RequestIDKey, reqID)
	handleError(c, &models.AppError{Code: models.ErrSessionExpired, Message: "Invalid token", Status: http.StatusUnauthorized})

	after := buf.String()
	nAfter := countErrorLines(after, sentinel)
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

	reqID, sentinel := requestIDSentinel()
	rawGETWithRequestID(t, srv.URL+"/api/ws/logs/stack-a", reqID)
	waitHandled(t, handled)

	got := buf.String()
	n := countErrorLines(got, sentinel)
	if n != 1 {
		t.Fatalf("a raw upgrade failure at logs.go (a silent call site) produced %d ERROR line(s) carrying %s, want exactly 1. captured = %q", n, sentinel, got)
	}
	if !strings.Contains(got, "not using the websocket protocol") {
		t.Errorf("the ERROR line doesn't carry the underlying cause. captured = %q", got)
	}
	if !strings.Contains(got, "UPGRADE_FAILED") {
		t.Errorf(`a raw HandshakeError (not a *models.AppError) must be coded "UPGRADE_FAILED". captured = %q`, got)
	}
}

// TestUpgradeFailure_HandleErrorSiteLogsOnce is the SAME raw-HandshakeError
// control at dashboard.go, one of the four sites that used to call
// handleError(c, err) after a failed serveWS (monitoring.go x2, dashboard.go,
// terminal.go). A raw HandshakeError is not a *models.AppError, so
// handleError's 500 fallback took it through logServerFault and this shape
// logged TWICE there: serveWS's own line (agent-os-94yx) plus handleError's.
// That double line was accepted as an interim state (the agent-os-ua4y
// precedent) until the call sites were free of other in-flight work; an
// earlier version of this test pinned the count at 2 on purpose. With the
// post-serveWS handleError calls removed (agent-os-lukw) the site logs
// exactly once, like the four sites that always returned silently.
//
// Seen failing first (as the count-2 -> count-1 flip): on the pre-fix tree
// `go build ./...` exits 0 and this fails on its ASSERTION with n = 2.
func TestUpgradeFailure_HandleErrorSiteLogsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cm := NewConnectionManager(4)
	srv, handled, _ := newDashboardAuthEnabledFixture(t, cm)

	reqID, sentinel := requestIDSentinel()
	rawGETWithRequestID(t, srv.URL+"/api/ws/dashboard/metrics", reqID)
	waitHandled(t, handled)

	got := buf.String()
	n := countErrorLines(got, sentinel)
	if n != 1 {
		t.Fatalf("a raw upgrade failure at dashboard.go (a former handleError call site) produced %d ERROR line(s) carrying %s, want exactly 1 "+
			"(serveWS's line only; handleError's 500 fallback no longer runs after serveWS, agent-os-lukw). captured = %q", n, sentinel, got)
	}
	if !strings.Contains(got, "UPGRADE_FAILED") {
		t.Errorf(`the one line must be serveWS's, coded "UPGRADE_FAILED". captured = %q`, got)
	}
}

// TestWSAuthFailure_HandleErrorSiteNeverWritesToTheHijackedConnection is the
// seen-failing-first regression for agent-os-lukw: the 401 auth-frame shape,
// driven end-to-end through dashboard.go (a former handleError call site).
//
// By the time upgradeConnection returns the 401 *models.AppError for a bad
// auth frame, upgrader.Upgrade has already hijacked the connection. The
// errWSRefused guard (agent-os-o1jp.1) kept a cap REFUSAL from reaching
// handleError for exactly that reason, but the 401 shape had no guard:
// handleError called c.JSON on the hijacked writer. That is deterministic,
// not a race: net/http's response.WriteHeader/Write see the hijacked flag,
// return ErrHijacked and report "http: response.Write on hijacked connection"
// through the server's ErrorLog, and gin's Context.Render then does
// `_ = c.Error(err); c.Abort()` (gin v1.12.0 context.go, OBSERVED).
//
// Three arms on one dial, and only the last two discriminate: the ERROR-line
// count is already 1 on the pre-fix tree (serveWS logs the failure; a 401
// through handleError is silent below 500, TestHandleError_4xxStaysSilent),
// so (a) is a guard against regressing agent-os-94yx, not the failing-first
// evidence. (b) the server's ErrorLog (httptest.Server.Config.ErrorLog wired
// to hijackProbe) contains a "hijacked connection" line pre-fix and nothing
// post-fix; (c) gin's c.Errors, read after the handler chain returned, is
// non-empty pre-fix and empty post-fix.
//
// Seen failing first: on the pre-fix tree `go build ./...` exits 0 and this
// test fails on its ASSERTIONS (b) and (c), not on a compile error.
func TestWSAuthFailure_HandleErrorSiteNeverWritesToTheHijackedConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	cm := NewConnectionManager(4)
	srv, handled, probe := newDashboardAuthEnabledFixture(t, cm)

	reqID, sentinel := requestIDSentinel()
	code, text := dialWSAndSendInvalidToken(t, srv, "/api/ws/dashboard/metrics", reqID)
	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeAuthFailure) -- the auth failure itself didn't happen the way this test assumes", code, text, CloseCodeAuthFailure)
	}
	waitHandled(t, handled)

	got := buf.String()
	if n := countErrorLines(got, sentinel); n != 1 {
		t.Errorf("a WS auth failure at dashboard.go produced %d ERROR line(s) carrying %s, want exactly 1 (serveWS's own). captured = %q", n, sentinel, got)
	}
	if !strings.Contains(got, "WebSocket upgrade failed") {
		t.Errorf("the ERROR line is not serveWS's. captured = %q", got)
	}
	if errLog := probe.ErrorLog(); strings.Contains(errLog, "hijacked connection") {
		t.Errorf("the handler wrote to a connection upgradeConnection had already hijacked. server ErrorLog = %q", errLog)
	}
	if n := probe.GinErrors(); n != 0 {
		t.Errorf("gin recorded %d error(s) on the context after a WS auth failure, want 0 (a failed write onto the hijacked writer is what puts them there)", n)
	}
}

// hijackProbe is the instrument for the 401-shape test above: the
// http.Server's ErrorLog sink (where net/http reports a write on a hijacked
// connection) and the number of gin errors the handler chain left on its
// context. Both are written on the server's handler goroutine and read on
// the test goroutine only after waitHandled, which gives the read its
// happens-before edge; the mutex is belt-and-braces for any other server
// goroutine that reports through ErrorLog.
type hijackProbe struct {
	mu        sync.Mutex
	errorLog  strings.Builder
	ginErrors int
}

func (p *hijackProbe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errorLog.Write(b)
}

func (p *hijackProbe) ErrorLog() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errorLog.String()
}

func (p *hijackProbe) GinErrors() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ginErrors
}

// rawGETWithRequestID is http.Get with an X-Request-ID header: a plain,
// non-WebSocket GET against a WS route makes upgrader.Upgrade itself fail,
// and the header makes serveWS's resulting log line carry the test's
// sentinel (agent-os-737f). The response is a 4xx and is not asserted on
// here; the tests read the log instead.
func rawGETWithRequestID(t *testing.T, url, requestID string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set(middleware.RequestIDHeader, requestID)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

// plantStrayUpgradeFailureLine models the hazard agent-os-737f pins: some
// goroutine this test never started or joined (a leak from an earlier test,
// net/http teardown) logging an ERROR through the shared default sink while
// this test's capture buffer is that sink. It logs serveWS's exact line
// shape (same msg, same code) under a request_id that is NOT this test's,
// from its own goroutine, and returns a channel closed after the write.
//
// The write is inside the capture window by construction: the goroutine
// starts after captureHandlerLogs swapped the sink, and the caller receives
// from the returned channel before reading the buffer, so the read is
// ordered after the write no matter how the scheduler interleaves it with
// the server goroutine. slog's TextHandler serialises concurrent Handle
// calls under its own mutex, so the two writers do not race each other
// (OBSERVED: the -race gate on this test stays clean with the plant in
// place); the channel close gives the test goroutine's read its
// happens-before edge, exactly as withHandlerDoneSignal does for the
// server's write.
func plantStrayUpgradeFailureLine(t *testing.T) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		slog.Default().Error("WebSocket upgrade failed",
			"request_id", uuid.NewString(),
			"code", "UPGRADE_FAILED",
			"error", "planted by agent-os-737f: a stray line from a goroutine this test did not synchronise with")
	}()
	return done
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
	router.Use(middleware.RequestID()) // puts the test's sentinel on serveWS's line (countErrorLines)
	handler.RegisterRoutes(router.Group("/api"))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, handled
}

// newDashboardAuthEnabledFixture is newDashboardCapFixture's twin with a
// done-signal middleware, for the same reason as newLogsAuthEnabledFixture
// above. authDisabled is false so the 401 auth-frame shape is reachable
// (the raw-HandshakeError shape fails before either auth branch and does
// not care). It also returns a hijackProbe: the server is built unstarted
// so its ErrorLog can be pointed at the probe before any connection is
// served (net/http reads Server.ErrorLog from serving goroutines, so
// setting it after Start would itself be a race), and a second middleware,
// registered AFTER withHandlerDoneSignal's so it runs inside it, records
// len(c.Errors) once the handler chain has returned.
func newDashboardAuthEnabledFixture(t *testing.T, cm *ConnectionManager) (*httptest.Server, <-chan struct{}, *hijackProbe) {
	t.Helper()

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewDashboardHandler(nil, docker, db, cm)

	probe := &hijackProbe{}
	router, handled := withHandlerDoneSignal(gin.New())
	router.Use(middleware.RequestID()) // puts the test's sentinel on serveWS's line (countErrorLines)
	router.Use(func(c *gin.Context) {
		c.Next()
		probe.mu.Lock()
		probe.ginErrors = len(c.Errors)
		probe.mu.Unlock()
	})
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", false)

	srv := httptest.NewUnstartedServer(router)
	srv.Config.ErrorLog = log.New(probe, "", 0)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, handled, probe
}

// dialWSAndSendInvalidToken completes a WebSocket handshake with no cookie
// (so upgradeConnection reads a JSON auth message instead), sends an
// obviously-invalid token, and returns the close code the server sends back.
// Same shape as readRefusalCloseCode (ws_cap_refusal_close_code_test.go),
// reused rather than adding a distinct dial helper, extended with the one
// extra step (WriteJSON) this bead's auth handshake needs. requestID goes
// out as X-Request-ID so the fixture's RequestID middleware stamps it on the
// context and serveWS's log line carries it (agent-os-737f).
func dialWSAndSendInvalidToken(t *testing.T, srv *httptest.Server, path, requestID string) (int, string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{middleware.RequestIDHeader: {requestID}})
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
