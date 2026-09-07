package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMonitoringMetricsWS_RevocationRouteClosesConnection pins
// waitForServerSideClose against the SECOND route to cm.Count() == 0, which
// the four tests in monitoring_metrics_close_test.go never exercise.
//
// There are two routes, with OPPOSITE orderings:
//
//   - Route 1, serveWS's release (ws.go:811-814): one sync.OnceFunc doing
//     Close() and then Remove(). Count() reaching 0 already implies a closed
//     socket, so any probe shape works here.
//   - Route 2, closeMatching (ws.go:278-315), reached from CloseAll (:205),
//     CloseForSession (:224) and CloseForUser (:256) — shutdown, logout and
//     password change. It deletes from cm.connections at :296 (Count() hits 0
//     THERE), unlocks, writes the close frames, sleeps `grace` at :307, and
//     only calls Conn.Close() at :312. CloseAll passes 100ms.
//
// On route 2 an emptied manager therefore does NOT imply a closed socket, and
// a probe taken a fixed beat after Count() == 0 reads a still-open socket and
// reports a leak that is not there. What exceeds that beat is a literal
// time.Sleep in production code, not network latency — so widening the beat is
// not the fix, and polling is: it is correct on both routes.
//
// CloseAll runs in a goroutine deliberately. Called synchronously it would
// return only after its own grace had elapsed and the socket was already
// closed, which is the one arrangement in which every probe shape passes and
// the test proves nothing. The goroutine reproduces the real shape — a
// shutdown or revocation closing connections while something else observes the
// manager — and is what puts the observation inside the grace window.
func TestMonitoringMetricsWS_RevocationRouteClosesConnection(t *testing.T) {
	srv := newFakeDockerMetricsServer(t, http.StatusOK, `[{"Id":"c1"}]`, streamingStatsHandler())
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	resp.Body.Close()

	serverConn := firstConnection(t, cm)

	// Drain one real metrics frame first. This parks the handler in its
	// streaming loop, which is the precondition firstConnection documents,
	// and proves the connection is live before it is revoked.
	require.NoError(t, clientConn.SetReadDeadline(hangGuardDeadline(t)))
	_, _, err := clientConn.ReadMessage()
	require.NoError(t, err, "expected a metrics frame before revoking")

	go cm.CloseAll()

	if !waitForServerSideClose(t, serverConn, cm) {
		t.Error("server-side connection was never closed after CloseAll (revocation route)")
	}
}
