package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// TestDashboardMetricsWS_UpgradeFailureLogsTheCause is the regression for
// agent-os-zaor: handleDashboardMetricsWebSocket reported a WebSocket
// upgrade failure with `_ = c.Error(err)`, and nothing in this codebase reads
// gin's c.Errors — main.go builds a bare gin.New() with no gin.Logger and no
// ErrorLogger, and middleware/logging.go never touches it. The failure was
// therefore 100% silent: no log line anywhere, in the one place an operator
// has nothing else to go on, because the socket never opened.
//
// The three sibling WS handlers (monitoring.go x2, terminal.go) call
// handleError under this same errWSRefused guard and so log via agent-os-7z8c;
// dashboard.go was the only divergence.
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
// Seen failing first: with `_ = c.Error(err)` restored, `go build ./...`
// exits 0 and this test fails on its ASSERTION with an empty buffer
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
	// NOT a *models.AppError and so takes handleError's 500 fallback.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/ws/dashboard/metrics", nil)

	h.handleDashboardMetricsWebSocket("test-secret-key-32-chars-long!!!", true)(c)

	got := buf.String()
	if !strings.Contains(got, "not using the websocket protocol") {
		t.Errorf("a WebSocket upgrade failure produced no log naming the cause; "+
			"the operator has no record at all that it happened. captured = %q", got)
	}
	if !strings.Contains(got, "level=ERROR") {
		t.Errorf("the upgrade failure was logged below ERROR; captured = %q", got)
	}
	// The sink with no reader must stay empty, or the defect is back in a
	// second copy alongside the fix.
	if len(c.Errors) != 0 {
		t.Errorf("the failure was recorded into gin's c.Errors, which nothing reads; got %d entry/entries", len(c.Errors))
	}
}
