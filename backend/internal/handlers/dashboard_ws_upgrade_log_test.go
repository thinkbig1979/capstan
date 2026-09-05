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
	requirePlantLanded(t, got)
	// The "handleError's line is back" claim (agent-os-lukw), discriminated on
	// this request's own id (agent-os-nho7). It used to be an absence check on
	// the bare substring "request failed" over captureHandlerLogs's buffer,
	// which is slog's PROCESS-GLOBAL sink for the test's duration, and
	// "request failed" is logServerFault's fixed msg for EVERY 5xx anywhere in
	// the binary — so any other goroutine's fault line turned this red.
	//
	// Counting instead of excluding: exactly ONE ERROR line may carry this
	// request's id, serveWS's own. handleError's line would carry the same id
	// (logServerFault stamps it from the same context) and make it two. Same
	// pin, same site, as TestUpgradeFailure_HandleErrorSiteLogsOnce
	// (ws_upgrade_failure_log_test.go).
	//
	// The count alone is not enough, and this is not hypothetical: a DOUBLE
	// mutation defeats it. Demote serveWS's line to WARN and let handleError's
	// ERROR line come back, and the count is still 1 while everything the test
	// exists to protect is gone — and the two old undiscriminated presence
	// checks that used to sit here (on "WebSocket upgrade failed" and on the
	// cause text, both against the whole buffer) would ALSO still pass, since
	// the demoted line carries both at WARN. So the assertions are made about
	// the LINE, not about the buffer: the one ERROR line for this request must
	// be serveWS's, and must carry the cause. OBSERVED red under exactly that
	// double mutation.
	lines := errorLinesFor(got, sentinel)
	if len(lines) != 1 {
		t.Errorf("the upgrade failure produced %d ERROR line(s) carrying %s, want exactly 1 (serveWS's own; "+
			"handleError's line must not be back after serveWS, agent-os-lukw). captured = %q", len(lines), sentinel, got)
		return
	}
	if !strings.Contains(lines[0], "WebSocket upgrade failed") {
		t.Errorf("the one ERROR line for this request is not serveWS's own (agent-os-94yx): %s", lines[0])
	}
	if !strings.Contains(lines[0], "not using the websocket protocol") {
		t.Errorf("the one ERROR line for this request does not name the cause; "+
			"the operator has no record of WHAT failed: %s", lines[0])
	}
	// The sink with no reader must stay empty, or the defect is back in a
	// second copy alongside the fix.
	if len(c.Errors) != 0 {
		t.Errorf("the failure was recorded into gin's c.Errors, which nothing reads; got %d entry/entries", len(c.Errors))
	}
}
