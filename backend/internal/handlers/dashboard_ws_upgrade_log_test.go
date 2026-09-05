package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// TestDashboardMetricsWS_UpgradeFailureLogsTheCause is the regression for
// agent-os-zaor: handleDashboardMetricsWebSocket reported a WebSocket
// upgrade failure with `_ = c.Error(err)`, and nothing in this codebase reads
// gin's c.Errors — main.go builds a bare gin.New() with no gin.Logger and no
// ErrorLogger, and middleware/logging.go never touches it. The failure was
// therefore 100% silent: no log line anywhere, in the one place an operator
// has nothing else to go on, because the socket never opened.
//
// The zaor fix routed the error through handleError, matching the three
// sibling sites at the time. The line asserted here is now serveWS's own
// "WebSocket upgrade failed" (agent-os-94yx): serveWS logs every
// upgrade/auth failure at all eight call sites, and the four post-serveWS
// handleError calls were removed (agent-os-lukw), so the handler itself
// logs nothing and must record nothing.
//
// Driven through gin.CreateTestContext rather than an httptest server on
// purpose: this failure happens BEFORE any hijack, so no real socket is
// needed, and staying on one goroutine means the plain bytes.Buffer that
// captureHandlerLogs hands back is read without a data race under -race.
//
// The sentinel is a substring of gorilla's own HandshakeError text, so it can
// only reach the buffer through the error chain — a future "sanitised" log
// line that emits status and code but drops the cause still fails here.
//
// Seen failing first (zaor): with `_ = c.Error(err)` restored, `go build
// ./...` exits 0 and this test fails on its ASSERTION with an empty buffer
// (`captured = ""`), not on a compile error.
func TestDashboardMetricsWS_UpgradeFailureLogsTheCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	// The nil-docker gate runs before serveWS, so h.docker only has to
	// construct (NewDockerService pings at construction); the upgrade fails
	// before any docker method is called.
	dockerSrv := newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil)
	docker := newTestDockerServiceAgainst(t, dockerSrv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	h := NewDashboardHandler(nil, docker, db, NewConnectionManager(4))

	// A plain GET with no Connection/Upgrade headers: upgrader.Upgrade
	// rejects the handshake and returns a *websocket.HandshakeError, which is
	// NOT a *models.AppError and so is logged by serveWS as UPGRADE_FAILED.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/ws/dashboard/metrics", nil)

	// No router here, so no RequestID middleware runs: the sentinel goes onto
	// the context directly, exactly as ws_upgrade_failure_log_test.go's
	// TestHandleError_NeverLogsA401AfterAWSAuthFailure does it. serveWS reads
	// it back through middleware.RequestIDFrom (ws.go, "request_id"), so its
	// ERROR line carries the sentinel and logServerFault's would too.
	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()
	c.Set(middleware.RequestIDKey, reqID)

	h.handleDashboardMetricsWebSocket("test-secret-key-32-chars-long!!!", true)(c)
	<-plantDone

	got := buf.String()
	if !strings.Contains(got, "not using the websocket protocol") {
		t.Errorf("a WebSocket upgrade failure produced no log naming the cause; "+
			"the operator has no record at all that it happened. captured = %q", got)
	}
	if !strings.Contains(got, "WebSocket upgrade failed") {
		t.Errorf("the line is not serveWS's own (agent-os-94yx); captured = %q", got)
	}
	// The "handleError's line is back" claim (agent-os-lukw), discriminated on
	// this request's own id (agent-os-nho7). It used to be an absence check on
	// the bare substring "request failed" over captureHandlerLogs's buffer,
	// which is slog's PROCESS-GLOBAL sink for the test's duration, and
	// "request failed" is logServerFault's fixed msg for EVERY 5xx anywhere in
	// the binary — so any other goroutine's fault line turned this red.
	//
	// Counting instead of excluding: exactly ONE ERROR line may carry this
	// request's id, serveWS's own. handleError's line would carry the same id
	// (logServerFault stamps it from the same context) and make it two. That
	// is strictly stronger than the old check — it also catches a second line
	// for this request that is neither of those two — and it subsumes the
	// separate level=ERROR presence assertion this replaces, since
	// countErrorLines only counts level=ERROR lines. Same pin, same site, as
	// TestUpgradeFailure_HandleErrorSiteLogsOnce (ws_upgrade_failure_log_test.go).
	if n := countErrorLines(got, sentinel); n != 1 {
		t.Errorf("the upgrade failure produced %d ERROR line(s) carrying %s, want exactly 1 (serveWS's own; "+
			"handleError's line must not be back after serveWS, agent-os-lukw). captured = %q", n, sentinel, got)
	}
	// The sink with no reader must stay empty, or the defect is back in a
	// second copy alongside the fix.
	if len(c.Errors) != 0 {
		t.Errorf("the failure was recorded into gin's c.Errors, which nothing reads; got %d entry/entries", len(c.Errors))
	}
}
