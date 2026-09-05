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
// would route a cap refusal through dashboard.go's error-reporting call — one
// made on a ResponseWriter upgrader.Upgrade has already hijacked.
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
// agent-os-zaor swapped that reporting call from `_ = c.Error(err)` to
// `handleError(c, err)` and this assertion SURVIVES the swap — verified by
// mutation, not assumed. c.Errors is still populated on the mutant, now by gin
// itself rather than by the handler: handleError's c.JSON renders onto the
// hijacked writer, the write returns http.ErrHijacked, and gin's Render path
// records it. Re-verified 2026-09-04 with the guard mutated to
// `if !errors.Is(err, errWSRefused) || true`:
//
//	dashboard_ws_refusal_test.go:101: gin recorded 1 error(s) after a cap refusal
//	dashboard_ws_refusal_test.go:105: a cap refusal drove output onto the hijacked
//	    connection: "http: response.Write on hijacked connection from
//	    github.com/gin-gonic/gin.(*responseWriter).Write (response_writer.go:86)"
//
// and PASS with the guard intact (captured log empty). The second assertion is
// the more direct one: it observes the actual harm — bytes aimed at a socket
// net/http no longer owns — instead of the bookkeeping side effect. It reads
// the STDLIB log, which reaches the buffer only because captureHandlerLogs
// leaves slog.SetDefault's log.SetOutput redirect in place for the test.
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
	// "anon:127.0.0.1", not the literal "anonymous": since agent-os-8uuw,
	// upgradeConnection assigns "anon:"+c.ClientIP() under AUTH_DISABLED, and
	// every httptest dial below is unauthenticated default-dialer traffic
	// from 127.0.0.1, so this is the identity that actually collides with it.
	require.NoError(t, cm.Add("already-open", &Connection{ID: "already-open", UserID: "anon:127.0.0.1"}))

	handler := NewDashboardHandler(nil, docker, db, cm)

	buf := captureHandlerLogs(t)

	// A recording middleware, not a direct read of the request's
	// gin.Context: the handler runs inside httptest.NewServer's own
	// goroutine, so this is the only way to observe c.Errors after the
	// handler returns.
	type refusalObservation struct {
		ginErrs int
		logs    string
	}
	observed := make(chan refusalObservation, 1)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		// Snapshot taken HERE, on the serving goroutine, and handed over the
		// channel: the buffer captureHandlerLogs returns is a plain
		// bytes.Buffer, so reading it from the test goroutine while this one
		// may still be writing is a data race under -race. The channel send
		// gives the read a happens-before edge.
		observed <- refusalObservation{ginErrs: len(c.Errors), logs: buf.String()}
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
	case obs := <-observed:
		if obs.ginErrs != 0 {
			t.Errorf("gin recorded %d error(s) after a cap refusal; want 0 — "+
				"a refusal must not be reported via c.Error on an already-hijacked connection", obs.ginErrs)
		}
		if strings.Contains(obs.logs, "hijacked connection") {
			t.Errorf("a cap refusal drove output onto the hijacked connection: %q", obs.logs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never returned within 5s")
	}
}
