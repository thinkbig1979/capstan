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
)

// TestDashboardMetricsWS_CapRefusalReportsNoGinError is the regression for a
// defect an adversary review found in agent-os-o1jp.1 itself: an earlier
// draft of serveWS collapsed its two error branches (upgrade/auth failure vs.
// a per-user cap refusal from cm.Add) into one naive `if err != nil`, which
// would route a cap refusal through dashboard.go's `_ = c.Error(err)` — a
// call made on a ResponseWriter upgrader.Upgrade has already hijacked.
//
// The client-visible close code cannot catch this: TestOperationsRefuses
// BeyondPerUserCap and TestTerminalRefusesBeyondPerUserCap both assert only
// on what the client sees (close code, session counts), and both pass
// whether or not the server additionally — harmlessly today, but wrongly —
// drives gin's error machinery after the hijack. This test asserts
// server-side state instead: a refusal must leave gin's own c.Errors empty,
// proving errWSRefused's guard (errors.Is(err, errWSRefused)) is actually
// exercised, not just present in the diff.
//
// Seen failing first: with the guard removed (`_ = c.Error(err)` called
// unconditionally on any serveWS error), this test observes a non-zero
// error count — see agent-os-o1jp.1's report for the verbatim red run.
func TestDashboardMetricsWS_CapRefusalReportsNoGinError(t *testing.T) {
	// The nil-docker gate runs before serveWS, so h.docker only needs to
	// construct successfully (NewDockerService pings at construction) — a
	// refusal returns before any docker method is ever called.
	dockerSrv := newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil)
	docker := newTestDockerServiceAgainst(t, dockerSrv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cm := NewConnectionManager(1)
	require.NoError(t, cm.Add("already-open", &Connection{ID: "already-open", UserID: "anonymous"}))

	handler := NewDashboardHandler(nil, docker, db, cm)

	// A recording middleware, not a direct read of the request's
	// gin.Context: the handler runs inside httptest.NewServer's own
	// goroutine, so this is the only way to observe c.Errors after the
	// handler returns.
	ginErrCount := make(chan int, 1)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		ginErrCount <- len(c.Errors)
	})
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	wsSrv := httptest.NewServer(router)
	t.Cleanup(wsSrv.Close)

	url := "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/dashboard/metrics"
	clientConn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer clientConn.Close()
	defer resp.Body.Close()

	select {
	case n := <-ginErrCount:
		if n != 0 {
			t.Errorf("gin recorded %d error(s) after a cap refusal; want 0 — "+
				"a refusal must not be reported via c.Error on an already-hijacked connection", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never returned within 5s")
	}
}
