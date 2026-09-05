package middleware

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-8tqd, the widest member of the class: AuthMiddleware's session
// lookup answered EVERY GetSession error, not only a missing row, with 401
// SESSION_EXPIRED and no log line. database.GetSession returns the bare Scan
// error and never (nil, nil) (database/users.go:119-128), so a closed, locked
// or decrypt-failing database turned every authenticated request into a silent
// "your session expired" — the frontend logs the operator out on that code
// (frontend/src/lib/api.ts) and the server recorded nothing at all.
//
// This middleware cannot use handlers' handleError/logServerFault: handlers
// imports middleware, so the reverse would be an import cycle. The 500 is
// written inline and logged with a direct slog.Error carrying logServerFault's
// exact message and attribute keys, so an operator greps both the same way.
//
// Both arms run on one instrument, and the sentinel is proven non-vacuous per
// test (requireDBFaultSentinel) rather than assumed: RequestID() is mounted
// BEFORE AuthMiddleware, mirroring production order (main.go), and echoes the
// id it assigned in the response header.

const dbFaultSecret = "test-secret-key-32-chars"

// dbFaultPlantMarker identifies a stray ERROR line this test planted in the
// shared sink itself, so a zero count is evidence of absence rather than
// evidence the capture never fired.
const dbFaultPlantMarker = "planted by agent-os-8tqd"

// plantStrayServerFault writes the stray line SYNCHRONOUSLY, unlike handlers'
// goroutine-based planter. Deliberate: captureSlog (proxytrust_test.go) hands
// back a plain *bytes.Buffer with no mutex of its own, so a second goroutine
// writing it while the test goroutine reads would be a genuine data race under
// -race. Running on the test goroutine still proves the instrument fires,
// which is the whole job of the plant.
func plantStrayServerFault() {
	slog.Default().Error("request failed",
		"request_id", uuid.NewString(),
		"status", 500,
		"code", "INTERNAL_ERROR",
		"error", dbFaultPlantMarker+": a stray line carrying a different request's id")
}

func dbFaultErrorLines(captured, mustContain string) []string {
	var lines []string
	for _, line := range strings.Split(captured, "\n") {
		if strings.Contains(line, "level=ERROR") && strings.Contains(line, mustContain) {
			lines = append(lines, line)
		}
	}
	return lines
}

func requireDBFaultPlantLanded(t *testing.T, captured string) {
	t.Helper()
	if n := len(dbFaultErrorLines(captured, dbFaultPlantMarker)); n != 1 {
		t.Fatalf("the planted stray ERROR line is not in the capture window (found %d, want 1): the assertions that follow prove nothing. captured = %q", n, captured)
	}
}

// requireDBFaultSentinel proves the discriminator reached the middleware:
// RequestID honours an inbound X-Request-ID only when it parses as a UUID
// (requestid.go), and echoes what it assigned in the response header.
func requireDBFaultSentinel(t *testing.T, w *httptest.ResponseRecorder, requestID string) {
	t.Helper()
	if got := w.Header().Get(RequestIDHeader); got != requestID {
		t.Fatalf("RequestID() did not honour the inbound sentinel: %s = %q, want %q — every ERROR-line assertion here would be vacuous", RequestIDHeader, got, requestID)
	}
}

// closedSessionDB builds the fault the same way handlers' faultyDB(t) does —
// open a fully migrated DB, close its connection — inline rather than by
// importing handlers' test package, which is not importable. The two-sided
// proof faultyDB carries in its own test is repeated here on THIS db: the
// failure must not be the not-found predicate, or the fault arm and the
// control arm would be exercising the same branch.
func closedSessionDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db to induce failure: %v", err)
	}
	_, err = db.GetSession("nope")
	if err == nil {
		t.Fatalf("the closed connection did not induce a failure")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the closed DB fails with the SAME predicate as ordinary not-found (sql.ErrNoRows): %v — the two arms would not be distinguishable", err)
	}
	return db
}

// healthySessionDB is the control's DB: migrated, reachable, and holding no
// session row, so GetSession returns sql.ErrNoRows.
func healthySessionDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.GetSession("session-id"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("healthy GetSession for a missing row did not return sql.ErrNoRows, got %v — the control is not exercising the not-found branch", err)
	}
	return db
}

// newSessionLookupGuardRouter returns the router and a pointer to a flag the
// PROTECTED handler sets when it runs.
//
// That flag is what pins the Abort, and no status assertion can substitute for
// it. gin keeps the first status written to the response: a middleware that
// wrote its 401 (or 500) and then failed to c.Abort() would let this handler
// run, its c.Status(http.StatusOK) would be a superfluous-WriteHeader no-op,
// the recorder would still read 401, and every status and body assertion in
// this file would stay green while the request had in fact reached a protected
// route unauthenticated. requireAborted is the only assertion here that can
// see that.
//
// Read without a mutex on purpose: gin serves an httptest request on the
// calling goroutine, so the write below and the read in the test happen on the
// same goroutine (OBSERVED: the -race gate on this package is clean).
func newSessionLookupGuardRouter(db *database.DB) (*gin.Engine, *bool) {
	gin.SetMode(gin.TestMode)
	reached := false
	r := gin.New()
	r.Use(RequestID()) // BEFORE AuthMiddleware, as main.go chains them
	r.Use(AuthMiddleware(db, dbFaultSecret, false, ""))
	r.GET("/api/v1/dashboard/stats", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	return r, &reached
}

// requireAborted asserts the guard stopped the chain rather than merely
// writing a response, on BOTH arms: a server fault must not admit the request
// any more than a missing session does.
func requireAborted(t *testing.T, reached *bool, what string) {
	t.Helper()
	if *reached {
		t.Fatalf("%s reached the protected handler: AuthMiddleware wrote its response but did not Abort, so the request ran unauthenticated. No status or body assertion in this file can see this — gin keeps the first status written.", what)
	}
}

func guardRequest(t *testing.T, r *gin.Engine, requestID, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(RequestIDHeader, requestID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware_SessionLookupDBFaultIs500WithLoggedCause(t *testing.T) {
	buf := captureSlog(t)
	plantStrayServerFault()

	r, reached := newSessionLookupGuardRouter(closedSessionDB(t))
	token := signGuardToken(t, dbFaultSecret, time.Now().Add(time.Hour))
	requestID := uuid.NewString()

	w := guardRequest(t, r, requestID, token)
	requireDBFaultSentinel(t, w, requestID)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a database fault is logging every authenticated request out as an expired session. body = %s", w.Code, w.Body.String())
	}
	payload := decodeErrorBody(t, w.Body.Bytes())
	if payload["code"] != "INTERNAL_ERROR" {
		t.Fatalf("code = %v, want INTERNAL_ERROR (%s)", payload["code"], w.Body.String())
	}

	captured := buf.String()
	requireDBFaultPlantLanded(t, captured)
	lines := dbFaultErrorLines(captured, "request_id="+requestID)
	// Exactly 1 because THIS router mounts only RequestID + AuthMiddleware.
	// A full production router produces TWO ERROR lines joined on the same
	// request_id for a 500: this one, and LoggingMiddleware's "HTTP request"
	// line, which selects slog.LevelError for any status >= 500
	// (logging.go:35-37) and carries "request_id", RequestIDFrom(c)
	// (logging.go:39-40). It does wrap this guard in production — main.go
	// chains RequestID -> LoggingMiddleware at :369-370, outside the
	// AuthMiddleware mounted on the protected group at :422. So do NOT copy
	// this "want 1" onto a full-router fixture; there the discriminated count
	// for a server fault is 2.
	if len(lines) != 1 {
		t.Fatalf("the session-lookup fault produced %d ERROR line(s) carrying this request's id, want exactly 1. captured = %q", len(lines), captured)
	}
	if !strings.Contains(lines[0], "cause=") {
		t.Fatalf("the ERROR line carries no cause= attribute, so the fault is still undiagnosable: %q", lines[0])
	}
	if !strings.Contains(lines[0], "database is closed") {
		t.Fatalf("the logged cause is not the underlying driver error: %q", lines[0])
	}
	// logServerFault's shape, matched key for key so one grep finds both.
	for _, want := range []string{`msg="request failed"`, "status=500", "code=INTERNAL_ERROR"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("the ERROR line does not match logServerFault's shape (missing %q): %q", want, lines[0])
		}
	}

	// LAST on purpose: every assertion above stays green under a guard that
	// writes its response and forgets to Abort, so this one has to be reached
	// to be worth anything.
	requireAborted(t, reached, "a session-lookup database fault")
}

func TestAuthMiddleware_MissingSessionStillIs401AndSilent(t *testing.T) {
	buf := captureSlog(t)
	plantStrayServerFault()

	r, reached := newSessionLookupGuardRouter(healthySessionDB(t))
	token := signGuardToken(t, dbFaultSecret, time.Now().Add(time.Hour))
	requestID := uuid.NewString()

	w := guardRequest(t, r, requestID, token)
	requireDBFaultSentinel(t, w, requestID)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: a session row that genuinely is not there is still session loss. body = %s", w.Code, w.Body.String())
	}
	payload := decodeErrorBody(t, w.Body.Bytes())
	if payload["code"] != "SESSION_EXPIRED" {
		t.Fatalf("code = %v, want SESSION_EXPIRED (%s)", payload["code"], w.Body.String())
	}
	if payload["message"] != "Session not found or expired" {
		t.Fatalf("the pre-existing 401 message changed: %v, want %q", payload["message"], "Session not found or expired")
	}

	captured := buf.String()
	requireDBFaultPlantLanded(t, captured)
	// Unlike the fault arm's count, this 0 survives a full production router:
	// LoggingMiddleware logs a 4xx at slog.LevelWarn, not Error
	// (logging.go:33-34), so its "HTTP request" line for this 401 is not an
	// ERROR line at all and cannot perturb the count either way.
	if n := len(dbFaultErrorLines(captured, "request_id="+requestID)); n != 0 {
		t.Fatalf("a genuinely missing session produced %d ERROR line(s) carrying this request's id, want 0. captured = %q", n, captured)
	}

	// LAST, for the same reason as the fault arm's.
	requireAborted(t, reached, "a genuinely missing session")
}
