package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// agent-os-8tqd: the 401 sibling of agent-os-7lg1/3h9x's 404 collapse.
//
// GetUserByID / GetSession / GetUserByUsername return the bare Scan error and
// never (nil, nil) (database/users.go:51-71, :119-128), so every site that
// wrote `if err != nil || x == nil { 401 SESSION_EXPIRED }` answered a closed,
// locked or decrypt-failing database with "your session expired": the frontend
// logged the operator out (frontend/src/lib/api.ts:97-117 calls logout() only
// for a 401 carrying a session-loss code) and NOTHING was recorded server-side.
//
// Each site below is pinned two-sided ON THE SAME INSTRUMENT:
//   - fault arm:   faultyDB(t) behind the endpoint -> 500 + exactly one ERROR
//                  line carrying THIS request's id and the driver cause.
//   - control arm: a healthy migrated DB with the row genuinely missing -> the
//                  EXACT pre-existing 401 body, and zero ERROR lines of our own.
//
// The ERROR-line counts are discriminated by the test's own request_id
// (agent-os-737f): captureHandlerLogs installs slog's PROCESS-GLOBAL default
// sink, so a bare level=ERROR count is perturbed by any other goroutine in the
// binary. Every router here mounts middleware.RequestID() FIRST and every
// request sends the sentinel as X-Request-ID, so the discriminator is actually
// present on the line under assertion — without that ordering the counts would
// be vacuously zero. requireSentinelHonoured proves it per HTTP test rather
// than assuming it; the two WebSocket tests prove it structurally instead
// (each asserts a count of exactly 1 line carrying the sentinel, which is 0 if
// RequestID never ran or refused the inbound header).

const dbFaultTestSecret = "test-secret-key-32-chars-long!!!"

// errEnvelope is models.AppError's client-facing JSON shape (models/errors.go).
type errEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeErrEnvelope(t *testing.T, raw []byte) errEnvelope {
	t.Helper()
	var e errEnvelope
	require.NoError(t, json.Unmarshal(raw, &e), "response body is not JSON: %s", raw)
	return e
}

// newSessionLookupRouter mounts RequestID() before everything else, mirroring
// production order (main.go: RequestID -> ... -> AuthMiddleware -> handler).
// userID == "" skips the auth-context stub, for the public login route.
func newSessionLookupRouter(userID string, register func(r *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	if userID != "" {
		r.Use(authContextMiddleware(userID))
	}
	register(r)
	return r
}

func doDBFaultRequest(t *testing.T, r *gin.Engine, method, path, body, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.RequestIDHeader, requestID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// requireSentinelHonoured is the anti-vacuity guard for every ERROR-line
// assertion in this file: RequestID() echoes the id it assigned in the
// response header, and it only honours an inbound X-Request-ID that parses as
// a UUID (middleware/requestid.go:28-35). If this header does not come back as
// OUR sentinel, then no log line can carry it either, and both a count of 1
// and a count of 0 would be meaningless.
func requireSentinelHonoured(t *testing.T, w *httptest.ResponseRecorder, requestID string) {
	t.Helper()
	if got := w.Header().Get(middleware.RequestIDHeader); got != requestID {
		t.Fatalf("RequestID() did not honour the inbound sentinel: %s = %q, want %q — every ERROR-line assertion in this test would be vacuous",
			middleware.RequestIDHeader, got, requestID)
	}
}

// requireOneServerFaultLine asserts exactly one ERROR line carries this
// request's sentinel, and that it carries the driver cause rather than only
// the sanitised client-facing message. "cause=" is logServerFault's
// attribute, appended ONLY when the AppError carries a Cause (respond.go) —
// never true before this conversion, always true after — so it is the
// discriminator that a pre-existing adjacent log line cannot satisfy.
func requireOneServerFaultLine(t *testing.T, captured, sentinel, what string) string {
	t.Helper()
	requirePlantLanded(t, captured)
	lines := errorLinesFor(captured, sentinel)
	if len(lines) != 1 {
		t.Fatalf("%s produced %d ERROR line(s) carrying %s, want exactly 1. captured = %q", what, len(lines), sentinel, captured)
	}
	if !strings.Contains(lines[0], "cause=") {
		t.Fatalf("%s logged an ERROR line without a cause= attribute, so the fault is still undiagnosable: %q", what, lines[0])
	}
	if !strings.Contains(lines[0], "database is closed") {
		t.Fatalf("%s logged a cause that is not the underlying driver error: %q", what, lines[0])
	}
	return lines[0]
}

// --- auth.go Me (GetUserByID) ------------------------------------------------

func TestMe_DBFaultIs500WithLoggedCause(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	h := NewAuthHandler(faultyDB(t), dbFaultTestSecret, false)
	r := newSessionLookupRouter("test-user-id", func(r *gin.Engine) { r.GET("/auth/me", h.Me) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/auth/me", "", id)
	<-plant

	requireSentinelHonoured(t, w, id)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a database fault is being reported to the operator as an expired session. body = %s", w.Code, w.Body.String())
	}
	requireOneServerFaultLine(t, buf.String(), sentinel, "GET /auth/me against a faulty DB")
}

func TestMe_MissingUserStillIs401AndSilent(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h := NewAuthHandler(db, dbFaultTestSecret, false)
	r := newSessionLookupRouter("no-such-user", func(r *gin.Engine) { r.GET("/auth/me", h.Me) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/auth/me", "", id)
	<-plant

	requireSentinelHonoured(t, w, id)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: a genuinely missing user row is still session loss. body = %s", w.Code, w.Body.String())
	}
	body := decodeErrEnvelope(t, w.Body.Bytes())
	if body.Code != "SESSION_EXPIRED" || body.Message != "User not found" {
		t.Fatalf("401 body changed: code = %q, message = %q; want %q / %q", body.Code, body.Message, "SESSION_EXPIRED", "User not found")
	}
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /auth/me with a healthy DB and no user row")
}

// --- auth.go VerifyPassword (GetUserByID) ------------------------------------

func TestVerifyPassword_DBFaultIs500WithLoggedCause(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	h := NewAuthHandler(faultyDB(t), dbFaultTestSecret, false)
	r := newSessionLookupRouter("test-user-id", func(r *gin.Engine) { r.POST("/auth/verify-password", h.VerifyPassword) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPost, "/auth/verify-password", `{"password":"OldPassword123!"}`, id)
	<-plant

	requireSentinelHonoured(t, w, id)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a database fault is being reported to the operator as an expired session. body = %s", w.Code, w.Body.String())
	}
	requireOneServerFaultLine(t, buf.String(), sentinel, "POST /auth/verify-password against a faulty DB")
}

func TestVerifyPassword_MissingUserStillIs401AndSilent(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h := NewAuthHandler(db, dbFaultTestSecret, false)
	r := newSessionLookupRouter("no-such-user", func(r *gin.Engine) { r.POST("/auth/verify-password", h.VerifyPassword) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPost, "/auth/verify-password", `{"password":"OldPassword123!"}`, id)
	<-plant

	requireSentinelHonoured(t, w, id)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401. body = %s", w.Code, w.Body.String())
	}
	body := decodeErrEnvelope(t, w.Body.Bytes())
	if body.Code != "SESSION_EXPIRED" || body.Message != "User not found" {
		t.Fatalf("401 body changed: code = %q, message = %q; want %q / %q", body.Code, body.Message, "SESSION_EXPIRED", "User not found")
	}
	requireNoOwnErrorLines(t, buf.String(), sentinel, "POST /auth/verify-password with a healthy DB and no user row")
}

// --- settings.go ChangePassword (GetUserByID) --------------------------------

const changePasswordBody = `{"currentPassword":"OldPassword123!","newPassword":"NewPassword456!"}`

func TestChangePassword_DBFaultIs500WithLoggedCause(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	h := NewSettingsHandler(faultyDB(t), "", dbFaultTestSecret, false, nil, nil)
	r := newSessionLookupRouter("test-user-id", func(r *gin.Engine) { r.PUT("/auth/password", h.ChangePassword) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut, "/auth/password", changePasswordBody, id)
	<-plant

	requireSentinelHonoured(t, w, id)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a database fault is being reported to the operator as an expired session. body = %s", w.Code, w.Body.String())
	}
	requireOneServerFaultLine(t, buf.String(), sentinel, "PUT /auth/password against a faulty DB")
}

func TestChangePassword_MissingUserStillIs401AndSilent(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h := NewSettingsHandler(db, "", dbFaultTestSecret, false, nil, nil)
	r := newSessionLookupRouter("no-such-user", func(r *gin.Engine) { r.PUT("/auth/password", h.ChangePassword) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut, "/auth/password", changePasswordBody, id)
	<-plant

	requireSentinelHonoured(t, w, id)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401. body = %s", w.Code, w.Body.String())
	}
	body := decodeErrEnvelope(t, w.Body.Bytes())
	if body.Code != "SESSION_EXPIRED" || body.Message != "User not found" {
		t.Fatalf("401 body changed: code = %q, message = %q; want %q / %q", body.Code, body.Message, "SESSION_EXPIRED", "User not found")
	}
	requireNoOwnErrorLines(t, buf.String(), sentinel, "PUT /auth/password with a healthy DB and no user row")
}

// --- auth.go Login (GetUserByUsername): LOG-ONLY, response frozen ------------

// loginArm runs one login against the supplied DB and returns the recorder and
// the captured log for that request alone.
func loginArm(t *testing.T, db *database.DB) (*httptest.ResponseRecorder, string, string) {
	t.Helper()
	h := NewAuthHandler(db, dbFaultTestSecret, false)
	r := newSessionLookupRouter("", func(r *gin.Engine) { r.POST("/auth/login", h.Login) })
	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPost, "/auth/login", `{"username":"ghost","password":"whatever"}`, id)
	requireSentinelHonoured(t, w, id)
	return w, sentinel, id
}

// TestLogin_DBFaultChangesTheLogAndNothingElse is the whole fix at auth.go's
// login site, stated as an equality rather than as a status assertion.
//
// The response MUST NOT change. auth.go's Login always runs a bcrypt
// comparison — against the real hash when the user resolved, against
// dummyBcryptHash when it did not — precisely so the user-exists and
// user-missing paths take comparable time and the username-enumeration timing
// oracle stays closed. A distinguishable 500 on the DB-fault arm would reopen
// it, so the fix here is log-only: both arms keep the identical 401
// UNAUTHORIZED "Invalid credentials" body, byte for byte, and only the ERROR
// line differs (one with the driver cause vs none).
//
// The comparison itself still runs on both arms by construction, not by
// timing: the inserted log statement sits between the getter and the
// `userExists := lookupErr == nil && user != nil` line, contains no return and
// assigns none of user / lookupErr / hashToCompare / userExists, so
// bcrypt.CompareHashAndPassword remains the unconditional call it already was.
func TestLogin_DBFaultChangesTheLogAndNothingElse(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	healthy, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = healthy.Close() })

	missingW, missingSentinel, _ := loginArm(t, healthy)
	faultW, faultSentinel, _ := loginArm(t, faultyDB(t))
	<-plant

	if faultW.Code != missingW.Code {
		t.Fatalf("the DB-fault arm answered %d and the missing-user arm %d: the login response must be indistinguishable between them (username-enumeration oracle)", faultW.Code, missingW.Code)
	}
	if faultW.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want the pre-existing 401 on both arms", faultW.Code)
	}
	if got, want := faultW.Body.String(), missingW.Body.String(); got != want {
		t.Fatalf("login response body differs between the DB-fault arm and the missing-user arm:\n fault   = %s\n missing = %s", got, want)
	}
	body := decodeErrEnvelope(t, faultW.Body.Bytes())
	if body.Code != "UNAUTHORIZED" || body.Message != "Invalid credentials" {
		t.Fatalf("login 401 body changed: code = %q, message = %q; want %q / %q", body.Code, body.Message, "UNAUTHORIZED", "Invalid credentials")
	}

	captured := buf.String()
	requireOneServerFaultLine(t, captured, faultSentinel, "POST /auth/login against a faulty DB")
	requireNoOwnErrorLines(t, captured, missingSentinel, "POST /auth/login with a healthy DB and no such username")
}

// --- ws.go authenticateToken (db.GetSession) ---------------------------------

// newWSAuthProbeServer mounts serveWS directly rather than a product WS
// handler: the site under test is upgradeConnection's auth branch, which runs
// before any handler-specific work, and every product route would otherwise
// drag its own Docker/stack fixtures into a test about session lookup. The
// registration values mirror a metered call site so a refusal could never send
// an RFC 6455-invalid close code.
func newWSAuthProbeServer(t *testing.T, db *database.DB) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	router, handled := withHandlerDoneSignal(gin.New())
	router.Use(middleware.RequestID())
	cm := NewConnectionManager(10)
	router.GET("/ws/probe", func(c *gin.Context) {
		conn, cleanup, err := serveWS(c, db, dbFaultTestSecret, false, cm, wsRegistration{
			refuseCode:   CloseCodeRateLimit,
			refuseReason: "Too many connections",
		})
		if err != nil {
			return
		}
		defer cleanup()
		_ = conn.Conn.Close()
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, handled
}

// dialWSAndSendToken is dialWSAndSendInvalidToken's twin for a token the test
// chooses, so the failure can be made to happen at the session lookup rather
// than at JWT parsing.
func dialWSAndSendToken(t *testing.T, srv *httptest.Server, path, requestID, token string) (int, string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{middleware.RequestIDHeader: {requestID}})
	require.NoError(t, err, "dialing %s", url)
	defer conn.Close()
	defer resp.Body.Close()

	require.NoError(t, conn.WriteJSON(map[string]string{"type": "auth", "token": token}))

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

// TestWSAuth_DBFaultClosesWith1011AndLogsTheCause pins both halves of the WS
// site. The status half is the class fix: the AppError authenticateToken
// returns for a non-ErrNoRows GetSession failure now carries Status 500 and
// the driver cause, so serveWS's existing "WebSocket upgrade failed" line
// (ws.go, errors.As + "cause", appErr.Cause) reports it instead of printing
// SESSION_EXPIRED with a nil cause.
//
// The close-code half is what makes the fix reach the operator: both callers
// of authenticateToken used to write CloseCodeAuthFailure (4401) for ANY
// error, and the frontend's shouldReconnectAfter (frontend/src/lib/ws.ts)
// suppresses the reconnect ladder on 4401/4429/4404 — so a transient DB fault
// left a dead stream until a page reload. 1011 is RFC 6455's "unexpected
// condition", is not in that suppression list, and is already this codebase's
// close code for a server-side WS fault (logs.go, monitoring.go, dashboard.go).
func TestWSAuth_DBFaultClosesWith1011AndLogsTheCause(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	srv, handled := newWSAuthProbeServer(t, faultyDB(t))
	id, sentinel := requestIDSentinel()
	token := generateTestToken("user-1", "admin", "session-1", dbFaultTestSecret)
	require.NotEmpty(t, token, "failed to sign the probe token")

	code, text := dialWSAndSendToken(t, srv, "/ws/probe", id, token)
	waitHandled(t, handled)
	<-plant

	if code != websocket.CloseInternalServerErr {
		t.Fatalf("close code = %d (%q), want %d (1011): a transient DB fault is closing as an auth failure, which the frontend's reconnect ladder suppresses", code, text, websocket.CloseInternalServerErr)
	}
	if text != "Auth check failed" {
		t.Fatalf("close reason = %q, want %q", text, "Auth check failed")
	}

	captured := buf.String()
	line := requireOneServerFaultLine(t, captured, sentinel, "a WebSocket auth against a faulty DB")
	if !strings.Contains(line, "code=INTERNAL_ERROR") {
		t.Fatalf("the upgrade-failure line still labels a DB fault as a session problem: %q", line)
	}
}

func TestWSAuth_MissingSessionStillCloses4401(t *testing.T) {
	buf := captureHandlerLogs(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	srv, handled := newWSAuthProbeServer(t, db)
	id, sentinel := requestIDSentinel()
	token := generateTestToken("user-1", "admin", "session-1", dbFaultTestSecret)
	require.NotEmpty(t, token, "failed to sign the probe token")

	code, text := dialWSAndSendToken(t, srv, "/ws/probe", id, token)
	waitHandled(t, handled)

	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d (CloseCodeAuthFailure): a session row that genuinely is not there is still auth failure", code, text, CloseCodeAuthFailure)
	}
	if text != "Auth failed" {
		t.Fatalf("close reason = %q, want %q", text, "Auth failed")
	}

	// serveWS logs exactly one line for EVERY upgrade/auth failure
	// (agent-os-94yx), so the control's count is 1, not 0 — what must NOT be
	// there is a server-fault cause. A count of 1 here is also this test's own
	// anti-vacuity proof that RequestID honoured the sentinel.
	captured := buf.String()
	lines := errorLinesFor(captured, sentinel)
	if len(lines) != 1 {
		t.Fatalf("a missing session produced %d ERROR line(s) carrying %s, want exactly 1 (serveWS's upgrade-failure line). captured = %q", len(lines), sentinel, captured)
	}
	if !strings.Contains(lines[0], "code=SESSION_EXPIRED") {
		t.Fatalf("a genuinely missing session must still be reported as SESSION_EXPIRED: %q", lines[0])
	}
	if strings.Contains(lines[0], "cause=") {
		t.Fatalf("a genuinely missing session must not carry a server-fault cause: %q", lines[0])
	}
}
