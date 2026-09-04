package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// readMetricsFrame reads one WebSocket DATA message off the client and decodes
// it as a MetricsFrame. Using ReadMessage (not the raw frame layer) is what
// makes the assertion unfakeable by the scaffolding around it: gorilla never
// returns control frames from ReadMessage — a ping, a pong or a close frame
// surfaces as an *error*, never as a message — so a handler that merely
// answers safePingLoop or writes a close frame cannot satisfy this. The
// Timestamp check pins it further: an empty frame the handler never built
// would decode to the zero MetricsFrame (agent-os-74rl).
func readMetricsFrame(t *testing.T, conn *websocket.Conn, within time.Duration, why string) MetricsFrame {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(within)))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err, why)

	var frame MetricsFrame
	require.NoError(t, json.Unmarshal(payload, &frame), "metrics frame did not decode: %s", payload)
	require.NotEmpty(t, frame.Timestamp, "metrics frame carried no timestamp: %s", payload)
	return frame
}

// TestMonitoringMetricsWS_EmptyContainerListStaysOpen is the regression test
// for agent-os-74rl: handleMetricsWebSocket had no len(containerIDs)==0
// guard, so for a stack whose container list came back empty it fell straight
// into StreamStats — whose own empty-list branch (services/monitor.go, the
// `close(statsChan); return statsChan, nil` early return) hands back an
// already-closed channel. The handler's `case batch, ok := <-statsChan: if
// !ok { return }` then fired immediately and the socket closed within a
// millisecond of opening. The frontend reconnects on close, so the observable
// symptom was a redial storm: the metrics WS opening and exiting roughly once
// per second, forever.
//
// The fix mirrors handleDashboardMetricsWebSocket, which has carried this
// guard all along: hold the socket open and emit an empty frame on a ticker,
// so the client sees "this stack has no containers to report" instead of a
// dead connection.
//
// Scope note: this test says nothing about *why* the list is empty for a
// stack whose containers exist — that is a separate defect (agent-os-fg55).
// It asserts only that an empty list keeps the socket alive.
func TestMonitoringMetricsWS_EmptyContainerListStaysOpen(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[]`, nil)
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	serverConn := firstConnection(t, cm)

	frame := readMetricsFrame(t, clientConn, 5*time.Second,
		"expected an empty metrics frame instead of an immediate close on an empty container list")
	require.Empty(t, frame.Containers, "empty container list must yield a frame with no containers")

	if isServerSideClosed(serverConn) {
		t.Fatal("server-side connection was closed even though the client was still connected")
	}
	if got := cm.Count(); got != 1 {
		t.Fatalf("connection manager count = %d, want 1 (socket must stay registered on an empty list)", got)
	}
}

// TestMonitoringMetricsWS_PopulatedListStillStreams is the control arm on the
// same instrument as the test above (agent-os-74rl). It is green BEFORE the
// fix as well as after — that is the point: it is what proves the empty-list
// guard did not break, short-circuit or swallow the populated path. Two
// containers rather than one so a fix that collapsed the batch would show up.
func TestMonitoringMetricsWS_PopulatedListStillStreams(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[{"Id":"c1"},{"Id":"c2"}]`, streamingStatsHandler())
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	serverConn := firstConnection(t, cm)

	frame := readMetricsFrame(t, clientConn, 5*time.Second,
		"expected a real metrics frame for a stack with two running containers")
	require.Len(t, frame.Containers, 2, "populated container list must still stream every container")

	if isServerSideClosed(serverConn) {
		t.Fatal("server-side connection was closed while still streaming a populated list")
	}
}
