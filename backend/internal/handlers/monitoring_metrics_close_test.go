package handlers

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	srv, _ := newFakeDockerMetricsServerHoldingList(t, listStatus, listBody, statsHandler, false)
	return srv
}

// newHeldFakeDockerMetricsServer is newFakeDockerMetricsServer with its
// /containers/json response HELD until the returned release func is called.
//
// WHY THIS EXISTS — it is the whole fix for agent-os-gs7r, and it is not a
// timing tweak. On the setup-error path the handler registers its connection
// in the ConnectionManager and then, on the very next statement, fails the
// container list and returns, which Closes and Removes it. MEASURED on this
// package with a tight-spin observer (-count=5 -race): the connection is
// visible in cm for 1.622ms / 4.456ms / 1.556ms / 1.338ms / 3.648ms. The test
// then has to observe a ~2ms EDGE, and it only ever managed it because its
// first poll happens microseconds after Dial returns — MEASURED, instrumented
// firstConnection, 10/10 instances found it on poll iteration 1, at 2.26us to
// 19.25us. Poll iteration 2, at 10ms, is already past the window every time.
//
// So NO BOUND FIXES THIS. A bigger constant and a hang-guard deadline are
// equally useless: the connection is not late, it is GONE. Proof rather than
// argument — injecting a 200ms delay before the first poll, i.e. GIVING the
// poller a head start, turns the two _SetupErrorClosesConnection tests red
// 6 times out of 6 on an IDLE box (loadavg 1.87 -> 3.68), with the same
// "no connection registered in cm within 5s" message and the same ~5.4s as
// the loaded flake. Load was never the cause; it only widened the gap
// between Dial returning and the first poll.
//
// Holding the list response parks the handler INSIDE its setup-error path, so
// the registration the test is waiting for is DURABLE rather than a race the
// test wins by microseconds. The exit path under test is unchanged — release
// lets the same 500 through, and the same close-then-remove runs.
func newHeldFakeDockerMetricsServer(t *testing.T, listStatus int, listBody string) (*httptest.Server, func()) {
	t.Helper()
	return newFakeDockerMetricsServerHoldingList(t, listStatus, listBody, nil, true)
}

// newFakeDockerMetricsServerHoldingList backs both constructors above. When
// hold is false the gate is pre-released, so the server behaves exactly as it
// did before agent-os-gs7r for the five call sites that do not need a gate.
func newFakeDockerMetricsServerHoldingList(t *testing.T, listStatus int, listBody string, statsHandler http.HandlerFunc, hold bool) (*httptest.Server, func()) {
	t.Helper()

	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	if !hold {
		release()
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/json"):
			// r.Context() is the second arm so a client that goes away while
			// the gate is still shut cannot wedge this goroutine.
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
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
	// Registered AFTER srv.Close, so LIFO runs it FIRST: a test that fails
	// before releasing must not leave srv.Close blocked on an in-flight
	// request that is still parked on the gate.
	t.Cleanup(release)
	return srv, release
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

// wsRegistrationHangGuardCeiling bounds the two waits below. It is NOT a
// latency budget and no passing run ever reaches it: both waits block on a
// CONDITION their caller has arranged to be durable, and every observed
// satisfaction is in microseconds to low milliseconds (see
// newHeldFakeDockerMetricsServer). 60s is a hang guard — the answer to "the
// signal never came at all", not to "the box is busy".
//
// NAMING, deliberate: agent-os-fzqb added a wsHangGuardCeiling /
// hangGuardDeadline pair with the same intent in backup_ws_cap_test.go, on a
// branch this one is not based on. Same package, so identical names would
// make main fail to build the moment both land. These are spelled differently
// on purpose; collapsing the two pairs into one is a post-merge cleanup, and
// is flagged to the orchestrator rather than done blind from here.
const wsRegistrationHangGuardCeiling = 60 * time.Second

// wsRegistrationHangGuard returns an absolute deadline for a wait that must
// NEVER fire in a correct run:
//
//	min(t.Deadline() - reporting margin, now + wsRegistrationHangGuardCeiling)
//
// Both halves are load-bearing.
//
// THE CEILING HALF, and why t.Deadline() alone is wrong here. VERIFIED by
// reading .github/workflows/backend.yml on this branch: the unit job runs
// `go test ./... -count=1` with NO -timeout (line 116) and the race job
// likewise (line 156), so Go's default 10m package timeout applies — and the
// unit job's own `timeout-minutes: 10` (line 72) is THE SAME NUMBER. A guard
// derived from t.Deadline() alone would fire around 9m55s, inside the window
// GitHub is already killing the runner, and a real hang would surface as a
// cancelled job with no test output. That is strictly worse to diagnose than
// the failure being removed here. The ceiling keeps a hang inside the job, as
// a named assertion.
//
// THE t.Deadline() HALF. A bare constant is picked against an imagined
// machine; deriving from the binary's own -timeout makes the guard respect
// what the invoker actually allowed. Its bool is FALSE under `-timeout 0`, a
// routine local invocation, which is why the ceiling has to stand alone.
//
// COST when a signal genuinely never arrives: up to the ceiling before it
// reports. Seconds, not minutes.
func wsRegistrationHangGuard(t *testing.T) time.Time {
	t.Helper()

	guard := time.Now().Add(wsRegistrationHangGuardCeiling)
	if d, ok := t.Deadline(); ok {
		if reportBy := d.Add(-5 * time.Second); reportBy.Before(guard) {
			guard = reportBy // room to report before the runtime's own panic
		}
	}
	// A -timeout shorter than the reporting margin would put the guard in the
	// past and fail instantly; a floor keeps the failure a real timeout rather
	// than an artefact of the margin.
	if floor := time.Now().Add(time.Second); guard.Before(floor) {
		guard = floor
	}
	return guard
}

// firstConnection grabs whatever *Connection is currently registered in cm.
// Direct access to the unexported map is deliberate and safe here (this file
// is `package handlers`, same as ws.go): it is the only way to reach the
// actual *websocket.Conn the handler is holding, which is what this test
// needs to inspect after the handler returns — cm.Remove deletes the map
// entry on return, but the *Connection object itself survives via this
// pointer, letting the test check its Conn after it has been evicted from cm.
//
// PRECONDITION THE CALLER OWNS (agent-os-gs7r). This waits for a connection to
// APPEAR; it cannot catch one that has already been Removed, and no deadline
// can. Every caller must therefore hold the handler somewhere that keeps the
// connection registered while this runs. Eight of the ten call sites get that
// for free — the handler is parked in a streaming or ticker loop. The two
// _SetupErrorClosesConnection sites do not, and they are exactly the two that
// flaked; they now hold the handler open with newHeldFakeDockerMetricsServer.
// A future caller that skips that will flake the same way, and widening the
// bound below will not save it.
func firstConnection(t *testing.T, cm *ConnectionManager) *Connection {
	t.Helper()
	guard := wsRegistrationHangGuard(t)
	for {
		cm.mu.RLock()
		for _, c := range cm.connections {
			cm.mu.RUnlock()
			return c
		}
		cm.mu.RUnlock()

		if !time.Now().Before(guard) {
			t.Fatal("no connection was ever registered in cm: the handler never reached " +
				"serveWS's registration step, or it had already returned and Removed the " +
				"connection before this wait began (see newHeldFakeDockerMetricsServer)")
		}
		time.Sleep(2 * time.Millisecond)
	}
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
	// Unlike firstConnection's, this condition is MONOTONE — once the handler
	// returns, cm.Count() stays 0 — so it genuinely cannot be missed and only
	// ever needed a hang guard rather than the 5s budget it used to carry
	// (agent-os-gs7r; the same class as agent-os-fzqb's tests, and the reason
	// this one never showed up in the flake corpus).
	guard := wsRegistrationHangGuard(t)
	for {
		if cm.Count() == 0 {
			// Close() (this fix) runs, via defer, before Remove() — but give
			// a short beat for the network-level close to complete.
			time.Sleep(20 * time.Millisecond)
			return isServerSideClosed(conn)
		}

		if !time.Now().Before(guard) {
			t.Fatal("handler never returned: cm never emptied")
		}
		time.Sleep(2 * time.Millisecond)
	}
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
	// Held, not plain: the handler registers its connection and then fails the
	// container list on the next statement, so an unheld server leaves the
	// test racing a ~2ms registration window (agent-os-gs7r).
	srv, releaseContainerList := newHeldFakeDockerMetricsServer(t, http.StatusInternalServerError, `{"message":"boom"}`)
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	// The handler is parked on the held list response here, so it is
	// registered and cannot have been Removed yet.
	serverConn := firstConnection(t, cm)

	// Now let the 500 through: this is the exact error exit the test asserts
	// on, unchanged.
	releaseContainerList()

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
