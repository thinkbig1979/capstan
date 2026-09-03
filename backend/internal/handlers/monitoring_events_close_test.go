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

// newEventsTestFixture stands up a real MonitoringHandler server for
// /api/ws/events, mirroring newMetricsTestFixture in
// monitoring_metrics_close_test.go (agent-os-14gr). Unlike the metrics
// handler, handleEventsWebSocket never touches Docker or the DB on this path
// (authDisabled skips the token lookup, and there is no per-connection
// GetContainersForStack-equivalent call before the loop), so no fake Docker
// server is needed here — an in-memory DB is wired up only because
// NewMonitoringHandler requires one.
func newEventsTestFixture(t *testing.T, cm *ConnectionManager, eb *EventBus) *httptest.Server {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewMonitoringHandler(nil, nil, db, cm, eb)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func dialEvents(t *testing.T, srv *httptest.Server) (*websocket.Conn, *http.Response) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/events"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	return conn, resp
}

// TestMonitoringEventsWS_ClientDisconnectClosesConnection is the regression
// test for agent-os-iz9w: handleEventsWebSocket registered every upgraded
// connection with the ConnectionManager but never called conn.Conn.Close()
// on any streaming exit path, leaking the fd and safePingLoop's goroutine
// once a client vanished.
//
// Of the four exits in the handler's select loop, only one is reachable
// today: ctx.Done() is dead code (nothing but the function's own deferred
// cancel() ever cancels ctx, same as agent-os-14gr's metrics handlers), and
// the eventChan-closed (`!ok`) branch is ALSO dead code here — eventChan is a
// fresh per-connection channel that only this handler's own deferred
// Unsubscribe ever closes, and that runs on function return, after the loop
// has already exited. So the only exit a client can actually drive is a
// failed write when it vanishes mid-stream, which this test exercises by
// broadcasting an event after killing the raw TCP connection.
func TestMonitoringEventsWS_ClientDisconnectClosesConnection(t *testing.T) {
	cm := NewConnectionManager(10)
	eb := NewEventBus()
	srv := newEventsTestFixture(t, cm, eb)

	clientConn, resp := dialEvents(t, srv)
	resp.Body.Close()

	serverConn := firstConnection(t, cm)

	// Simulate the client vanishing: close the raw TCP connection without a
	// WebSocket close handshake.
	clientConn.Close()

	require.Eventually(t, func() bool { return eb.SubscriberCount() == 1 }, 2*time.Second, 10*time.Millisecond,
		"handler never subscribed to the event bus")

	// The handler has no reader goroutine to notice the disconnect directly
	// — a write is what discovers it. A single broadcast is not reliable:
	// the first write after a client vanishes can succeed into the kernel's
	// socket buffer with no immediate error, same as a real dropped
	// connection. Keep broadcasting (as StreamStats' ticker does for the
	// metrics-handler equivalent test) until the handler actually returns or
	// this goroutine is torn down at test end, so a real write failure is
	// guaranteed to surface within the wait window below.
	stopBroadcasting := make(chan struct{})
	defer close(stopBroadcasting)
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopBroadcasting:
				return
			case <-ticker.C:
				eb.Broadcast(models.StackEvent{Type: "test", Timestamp: time.Now()})
			}
		}
	}()

	if !waitForServerSideClose(t, serverConn, cm) {
		t.Error("server-side connection was never closed after the client vanished mid-stream")
	}
}

// TestMonitoringEventsWS_StillStreamingStaysOpen is the control: a fix that
// closes the connection unconditionally (e.g. right after upgrade, before
// any events are ever sent) would pass the test above for the wrong reason.
// A connection that is still open and subscribed must stay open and
// registered even after successfully receiving an event.
func TestMonitoringEventsWS_StillStreamingStaysOpen(t *testing.T) {
	cm := NewConnectionManager(10)
	eb := NewEventBus()
	srv := newEventsTestFixture(t, cm, eb)

	clientConn, resp := dialEvents(t, srv)
	defer clientConn.Close()
	defer resp.Body.Close()

	serverConn := firstConnection(t, cm)

	require.Eventually(t, func() bool { return eb.SubscriberCount() == 1 }, 2*time.Second, 10*time.Millisecond,
		"handler never subscribed to the event bus")

	eb.Broadcast(models.StackEvent{Type: "test", Timestamp: time.Now()})

	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected the broadcast event while still connected")

	if isServerSideClosed(serverConn) {
		t.Fatal("server-side connection was closed while the client was still connected and streaming")
	}
	if got := cm.Count(); got != 1 {
		t.Fatalf("connection manager count = %d, want 1 (still streaming)", got)
	}
}
