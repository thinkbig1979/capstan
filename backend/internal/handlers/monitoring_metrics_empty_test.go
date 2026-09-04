package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// readMetricsFrame reads one WebSocket DATA message off the client and returns
// both the decoded MetricsFrame and the raw wire payload. Using ReadMessage
// (not the raw frame layer) is what makes the assertion unfakeable by the
// scaffolding around it: gorilla never returns control frames from
// ReadMessage — a ping, a pong or a close frame surfaces as an *error*, never
// as a message — so a handler that merely answers safePingLoop or writes a
// close frame cannot satisfy this. The Timestamp check pins it further: an
// empty frame the handler never built would decode to the zero MetricsFrame.
//
// The raw payload is returned because the decoded struct erases the one
// distinction the empty-list test most needs: json.Unmarshal turns both
// `null` and `[]` into a zero-length Go slice, so no assertion on
// frame.Containers can tell them apart. Only the bytes can (agent-os-74rl).
func readMetricsFrame(t *testing.T, conn *websocket.Conn, within time.Duration, why string) (MetricsFrame, []byte) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(within)))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err, why)

	var frame MetricsFrame
	require.NoError(t, json.Unmarshal(payload, &frame), "metrics frame did not decode: %s", payload)
	require.NotEmpty(t, frame.Timestamp, "metrics frame carried no timestamp: %s", payload)
	return frame, payload
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
// The fix takes handleDashboardMetricsWebSocket's STRUCTURE — guard before
// StreamStats, hold the socket open, emit one frame per tick — but not its
// empty value. That handler sends `Containers: nil`, which is its own live
// defect (agent-os-5scv); this one sends an empty slice, and the wire-format
// assertion below is what pins the difference.
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

	frame, payload := readMetricsFrame(t, clientConn, 5*time.Second,
		"expected an empty metrics frame instead of an immediate close on an empty container list")
	require.Empty(t, frame.Containers, "empty container list must yield a frame with no containers")

	// The wire format, not the decoded struct, is the load-bearing assertion.
	// A nil Go slice marshals to `"containers":null`, and the frontend hook
	// consuming this socket calls .forEach on the field with no null guard
	// (frontend/src/hooks/useMetricsBase.ts:60, typed non-nullable at :25),
	// so a null here throws a TypeError in the browser every tick — trading a
	// loud redial storm for a silent client-side crash. json.Unmarshal decodes
	// both null and [] to a zero-length slice, so require.Empty above passes
	// either way and cannot catch this; only the bytes can.
	require.Contains(t, string(payload), `"containers":[]`,
		"empty frame must serialise containers as [] for the unguarded frontend forEach")
	require.NotContains(t, string(payload), `"containers":null`,
		"empty frame must not serialise containers as null")

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

	frame, _ := readMetricsFrame(t, clientConn, 5*time.Second,
		"expected a real metrics frame for a stack with two running containers")
	require.Len(t, frame.Containers, 2, "populated container list must still stream every container")

	if isServerSideClosed(serverConn) {
		t.Fatal("server-side connection was closed while still streaming a populated list")
	}
}

// TestMonitoringMetricsWS_EmptyListClientDisconnectClosesConnection pins the
// invariant the 2s-ticker comment in monitoring.go argues for but nothing
// previously enforced (agent-os-74rl): on the empty-list path a failed write
// is the ONLY client-disconnect detector, so the `return` in that loop's
// safeWriteJSON error branch is load-bearing. Turn it into a `continue` and
// the handler goroutine and its ConnectionManager slot survive the client
// forever — a leak, and exactly the failure the comment warns about.
//
// The populated path has had this coverage all along via
// TestMonitoringMetricsWS_ClientDisconnectClosesConnection in
// monitoring_metrics_close_test.go; the empty path had none, so the whole
// argument was aspirational. Mutation-checked rather than assumed: run under
// `go test -overlay` with that `return` swapped for `continue` and this test
// fails at waitForServerSideClose's "handler never returned" fatal, which is
// what proves it can fire at all.
//
// It also closes the loop on agent-os-14gr for a path that did not exist when
// 14gr was written: every exit from these handlers must close the server-side
// socket, and this new ticker loop added one.
func TestMonitoringMetricsWS_EmptyListClientDisconnectClosesConnection(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[]`, nil)
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	resp.Body.Close()

	serverConn := firstConnection(t, cm)

	// Drain one empty frame first, so the handler is provably parked in the
	// ticker loop — not still in setup — before the client vanishes.
	frame, _ := readMetricsFrame(t, clientConn, 5*time.Second,
		"expected an empty metrics frame before disconnecting")
	require.Empty(t, frame.Containers, "expected the empty-list frame, not a populated one")

	// Simulate the client vanishing: close the raw TCP connection with no
	// WebSocket close handshake, the same shape as a closed browser tab.
	clientConn.Close()

	if !waitForServerSideClose(t, serverConn, cm) {
		t.Error("server-side connection was never closed after the client vanished on the empty-list path")
	}
}
