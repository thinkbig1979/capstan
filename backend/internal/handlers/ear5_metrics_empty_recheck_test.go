package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// newFlippingFakeDockerServer stands in for the Docker Engine HTTP API the
// way newFakeDockerMetricsServer (monitoring_metrics_close_test.go) does, but
// its /containers/json response FLIPS: the first `emptyResponses` calls
// return an empty list, every call after that returns one running container.
// This is the agent-os-ear5 adversary shape: a host that starts empty and
// stops being empty while the socket is already open, driving the empty-host
// branch's re-check rather than a static host that can never exercise it.
func newFlippingFakeDockerServer(t *testing.T, emptyResponses int32) *httptest.Server {
	t.Helper()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/json"):
			n := atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if n <= emptyResponses {
				_, _ = w.Write([]byte(`[]`))
			} else {
				_, _ = w.Write([]byte(`[{"Id":"c1","State":"running"}]`))
			}
		case strings.Contains(r.URL.Path, "/stats"):
			streamingStatsHandler()(w, r)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// readFramesUntil reads metrics frames off conn until match(raw) is true or
// the deadline passes, returning the matching raw frame. t.Fatal on timeout.
func readFramesUntil(t *testing.T, conn *websocket.Conn, deadline time.Time, what string, match func(raw []byte) bool) []byte {
	t.Helper()
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("%s: deadline exceeded waiting for a matching frame", what)
		}
		require.NoError(t, conn.SetReadDeadline(deadline))
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "%s: reading frame", what)
		if match(raw) {
			return raw
		}
	}
}

// TestMonitoringMetricsWS_EmptyHostThenPopulated_LaterFrameCarriesContainer
// pins agent-os-ear5's monitoring.go site: handleMetricsWebSocket used to
// fetch the container list EXACTLY ONCE at connect and, if empty, never
// re-check — a container started after connect never appeared for the life
// of the socket. This drives the empty-host branch with a host that flips to
// non-empty while the socket is already open (a real dial, not a static
// fixture — a test against a static host cannot fail here) and asserts a
// LATER frame carries the container.
//
// Seen failing first against pre-fix monitoring.go: the read below timed out
// waiting for a matching frame — the ticker loop emitted `"containers":[]`
// forever regardless of the fake server's flip, because nothing in that
// branch ever called GetContainersForStack again.
func TestMonitoringMetricsWS_EmptyHostThenPopulated_LaterFrameCarriesContainer(t *testing.T) {
	srv := newFlippingFakeDockerServer(t, 1) // connect-time check empty; first re-check onward populated
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	deadline := wsRegistrationHangGuard(t)
	raw := readFramesUntil(t, clientConn, deadline, "monitoring empty-then-populated", func(raw []byte) bool {
		return bytes.Contains(raw, []byte(`"containerId":"c1"`))
	})
	t.Logf("first frame carrying the container: %s", raw)
}

// TestMonitoringMetricsWS_StaysEmptyIfHostStaysEmpty is the required
// two-sided control: a host that genuinely never stops being empty must keep
// emitting empty frames forever — proving the re-check fix discriminates
// rather than always eventually reporting containers regardless of host
// state. Reads several frames past the connect-time check to prove the
// re-check itself ran and still correctly reported empty.
func TestMonitoringMetricsWS_StaysEmptyIfHostStaysEmpty(t *testing.T) {
	srv := newFlippingFakeDockerServer(t, 1<<30) // never flips within this test
	monitor := newFakeMonitorService(t, srv)
	cm := NewConnectionManager(10)
	wsSrv := newMetricsTestFixture(t, monitor, cm)

	clientConn, resp := dialMetrics(t, wsSrv)
	defer clientConn.Close()
	defer resp.Body.Close()

	// Read a FIXED number of frames, each with its OWN fresh per-read
	// deadline, rather than racing a single shared wall-clock deadline across
	// reads (a shared deadline that reads have already spent most of leaves
	// the last read with too little of it remaining and fails on a timeout
	// that has nothing to do with the fix). 3 frames spans more than one 2s
	// ticker interval, so at least one re-check genuinely ran.
	const framesToRead = 3
	for i := 0; i < framesToRead; i++ {
		require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
		_, raw, err := clientConn.ReadMessage()
		require.NoError(t, err, "reading empty-host frame %d", i)
		if bytes.Contains(raw, []byte(`"containerId"`)) {
			t.Fatalf("host never stopped being empty but frame %d carried a container: %s", i, raw)
		}
		if !bytes.Contains(raw, []byte(`"containers":[]`)) {
			t.Fatalf("empty-host frame %d did not carry \"containers\":[]: %s", i, raw)
		}
	}
}

// TestDashboardMetricsWS_EmptyHostThenPopulated_LaterFrameCarriesContainer is
// the dashboard.go sibling of the monitoring.go test above — agent-os-ear5 is
// explicit that this is a CLASS spanning both handlers, and a fix to one
// alone leaves the other. Same shape, using GetRunningContainerIDs's fake
// server instead of GetContainersForStack's.
func TestDashboardMetricsWS_EmptyHostThenPopulated_LaterFrameCarriesContainer(t *testing.T) {
	srv := newFlippingFakeDockerServer(t, 1)
	docker := newTestDockerServiceAgainst(t, srv)
	monitor := newFakeMonitorService(t, srv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cm := NewConnectionManager(10)
	handler := NewDashboardHandler(monitor, docker, db, cm)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	wsSrv := httptest.NewServer(router)
	t.Cleanup(wsSrv.Close)

	url := "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/dashboard/metrics"
	clientConn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer clientConn.Close()
	defer resp.Body.Close()

	deadline := time.Now().Add(10 * time.Second)
	raw := readFramesUntil(t, clientConn, deadline, "dashboard empty-then-populated", func(raw []byte) bool {
		return bytes.Contains(raw, []byte(`"containerId":"c1"`))
	})
	t.Logf("first frame carrying the container: %s", raw)
}

// TestDashboardMetricsWS_StaysEmptyIfHostStaysEmpty is the dashboard.go
// two-sided control, mirroring TestMonitoringMetricsWS_StaysEmptyIfHostStaysEmpty.
func TestDashboardMetricsWS_StaysEmptyIfHostStaysEmpty(t *testing.T) {
	srv := newFlippingFakeDockerServer(t, 1<<30)
	docker := newTestDockerServiceAgainst(t, srv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cm := NewConnectionManager(10)
	handler := NewDashboardHandler(nil, docker, db, cm)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	wsSrv := httptest.NewServer(router)
	t.Cleanup(wsSrv.Close)

	url := "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/dashboard/metrics"
	clientConn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer clientConn.Close()
	defer resp.Body.Close()

	// See the monitoring.go control test's comment: a fixed frame count with a
	// fresh per-read deadline, not a shared wall-clock deadline across reads.
	const framesToRead = 3
	for i := 0; i < framesToRead; i++ {
		require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
		_, raw, err := clientConn.ReadMessage()
		require.NoError(t, err, "reading empty-host frame %d", i)
		if bytes.Contains(raw, []byte(`"containerId"`)) {
			t.Fatalf("host never stopped being empty but frame %d carried a container: %s", i, raw)
		}
		if !bytes.Contains(raw, []byte(`"containers":[]`)) {
			t.Fatalf("empty-host frame %d did not carry \"containers\":[]: %s", i, raw)
		}
	}
}
