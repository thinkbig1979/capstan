package handlers

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// newFakeDockerMetricsServer stands in for the Docker Engine HTTP API — just
// enough for MonitorService.GetContainersForStack and StreamStats to run
// their real code with no live daemon (agent-os-14gr). listStatus/listBody
// drive the /containers/json response (a non-200 is how the "setup error"
// exit path below is reached); statsHandler drives every
// /containers/{id}/stats response.
func newFakeDockerMetricsServer(t *testing.T, listStatus int, listBody string, statsHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(listStatus)
			_, _ = w.Write([]byte(listBody))
		case strings.Contains(r.URL.Path, "/stats"):
			if statsHandler != nil {
				statsHandler(w, r)
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newFakeMonitorService points a real *services.MonitorService at the fake
// server above, via docker/docker's own client — no live daemon needed, the
// client just dials the fake server's plain-HTTP loopback address.
func newFakeMonitorService(t *testing.T, srv *httptest.Server) *services.MonitorService {
	t.Helper()
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://" + srv.Listener.Addr().String()))
	require.NoError(t, err)
	return services.NewMonitorService(cli)
}

// streamingStatsHandler keeps writing one stats frame per tick until the
// request context ends, so StreamStats' per-container goroutine — and the
// handler's 1s-ticker main loop above it — stay genuinely alive for the
// duration of the test, the same as a real running container would.
func streamingStatsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if _, err := w.Write([]byte(`{"read":"2025-01-01T00:00:00Z"}` + "\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

// newMetricsTestFixture stands up a real MonitoringHandler server for
// /ws/metrics/:id, backed by the given (fake-docker-connected) monitor, so
// tests dial a genuine WebSocket connection and drive the real handler code.
func newMetricsTestFixture(t *testing.T, monitor *services.MonitorService, cm *ConnectionManager) *httptest.Server {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/metrics-dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "metrics-stack", Directory: "/test/metrics-dir", ComposeFile: "compose.yaml",
		ProjectName: "metrics-proj", Status: "running",
	}))

	handler := NewMonitoringHandler(monitor, nil, db, cm, NewEventBus())
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// firstConnection grabs whatever *Connection is currently registered in cm.
// Direct access to the unexported map is deliberate and safe here (this file
// is `package handlers`, same as ws.go): it is the only way to reach the
// actual *websocket.Conn the handler is holding, which is what this test
// needs to inspect after the handler returns — cm.Remove deletes the map
// entry on return, but the *Connection object itself survives via this
// pointer, letting the test check its Conn after it has been evicted from cm.
func firstConnection(t *testing.T, cm *ConnectionManager) *Connection {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cm.mu.RLock()
		for _, c := range cm.connections {
			cm.mu.RUnlock()
			return c
		}
		cm.mu.RUnlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no connection registered in cm within 5s")
	return nil
}

// isServerSideClosed reports whether the handler's underlying network
// connection has actually been closed. SetReadDeadline on an already-closed
// net.Conn fails with net.ErrClosed (verified against a plain net.Conn pair
// before relying on it here) — checking the gorilla frame layer would not be
// enough, since the bug this guards is exactly that a close FRAME can be sent
// (safeWriteCloseMessage) while the socket underneath is never Close()'d.
func isServerSideClosed(conn *Connection) bool {
	nc := conn.Conn.UnderlyingConn()
	err := nc.SetReadDeadline(time.Now())
	return errors.Is(err, net.ErrClosed)
}

// waitForServerSideClose waits for the handler goroutine to return (cm empties
// via its deferred Remove) and then reports whether the connection was
// actually closed by that point.
func waitForServerSideClose(t *testing.T, conn *Connection, cm *ConnectionManager) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cm.Count() == 0 {
			// Close() (this fix) runs, via defer, before Remove() — but give
			// a short beat for the network-level close to complete.
			time.Sleep(20 * time.Millisecond)
			return isServerSideClosed(conn)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("handler never returned (cm never emptied) within 5s")
	return false
}

func dialMetrics(t *testing.T, srv *httptest.Server) (*websocket.Conn, *http.Response) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/metrics/metrics-stack"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	return conn, resp
}

// TestMonitoringMetricsWS_SetupErrorClosesConnection is the regression test
// for agent-os-14gr's first reachable leak path: GetContainersForStack
// failing sends the client a close FRAME (safeWriteCloseMessage) but, before
// the fix, never closes the server-side socket.
//
// Contrary to the original bead brief, the handler's `case <-ctx.Done():
// return` is NOT reachable here — nothing in the current code ever calls
// cancel() except the function's own deferred call on return, so it cannot be
// exercised as a distinct "normal" exit. This test and the two below cover
// the exit paths that genuinely are reachable today.
func TestMonitoringMetricsWS_SetupErrorClosesConnection(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusInternalServerError, `{"message":"boom"}`, nil)
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	serverConn := firstConnection(t, cm)

	if !waitForServerSideClose(t, serverConn, cm) {
		t.Error("server-side connection was never closed after the GetContainersForStack error path returned")
	}
}

// TestMonitoringMetricsWS_ClientDisconnectClosesConnection is the regression
// test for the realistic case the leak actually matters for: a client vanishes
// mid-stream. The handler has no reader goroutine to notice this directly —
// StreamStats' 1s ticker driving the next scheduled write is what discovers
// it (the write fails), which is also the second, distinct exit path
// (write-error, not setup-error) required for a "two-sided" instrument.
func TestMonitoringMetricsWS_ClientDisconnectClosesConnection(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[{"Id":"c1"}]`, streamingStatsHandler())
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	resp.Body.Close()

	serverConn := firstConnection(t, cm)

	// Drain one real metrics frame first, proving the handler reached its
	// main streaming loop (past GetContainersForStack/StreamStats setup)
	// before simulating the client vanishing.
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected at least one metrics frame before disconnecting")

	// Simulate the client vanishing: close the raw TCP connection without a
	// WebSocket close handshake.
	clientConn.Close()

	if !waitForServerSideClose(t, serverConn, cm) {
		t.Error("server-side connection was never closed after the client vanished mid-stream")
	}
}

// TestMonitoringMetricsWS_StillStreamingStaysOpen is the control for the two
// tests above: a fix that closes the connection unconditionally (e.g. right
// after upgrade, before streaming even starts) would pass both of them for
// the wrong reason. A connection that is still actively streaming must stay
// open and registered.
func TestMonitoringMetricsWS_StillStreamingStaysOpen(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[{"Id":"c1"}]`, streamingStatsHandler())
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	serverConn := firstConnection(t, cm)

	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected at least one metrics frame while still connected")

	if isServerSideClosed(serverConn) {
		t.Fatal("server-side connection was closed while the client was still connected and streaming")
	}
	if got := cm.Count(); got != 1 {
		t.Fatalf("connection manager count = %d, want 1 (still streaming)", got)
	}
}
