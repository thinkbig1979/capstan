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

// metricsDrainRoute is one of the two timer-writing metrics sockets
// agent-os-q48p routes through watchPeer, on one of its two loops: the
// streaming loop (a running container) or the empty-host ticker loop.
type metricsDrainRoute struct {
	name string
	// open stands up the route against a fake Docker daemon and returns the
	// socket URL and the ConnectionManager its slots count in.
	open func(t *testing.T) (url string, cm *ConnectionManager)
}

func metricsDrainRoutes() []metricsDrainRoute {
	metrics := func(listBody string) func(t *testing.T) (string, *ConnectionManager) {
		return func(t *testing.T) (string, *ConnectionManager) {
			srv := newFakeDockerMetricsServer(t, http.StatusOK, listBody, streamingStatsHandler())
			cm := NewConnectionManager(10)
			wsSrv := newMetricsTestFixture(t, newFakeMonitorService(t, srv), cm)
			return "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/metrics/metrics-stack", cm
		}
	}
	dashboard := func(listBody string) func(t *testing.T) (string, *ConnectionManager) {
		return func(t *testing.T) (string, *ConnectionManager) {
			srv := newFakeDockerMetricsServer(t, http.StatusOK, listBody, streamingStatsHandler())
			db, err := database.NewWithMigrations(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { db.Close() })
			cm := NewConnectionManager(10)
			handler := NewDashboardHandler(newFakeMonitorService(t, srv), newTestDockerServiceAgainst(t, srv), db, cm)
			router := gin.New()
			handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
			wsSrv := httptest.NewServer(router)
			t.Cleanup(wsSrv.Close)
			return "ws" + strings.TrimPrefix(wsSrv.URL, "http") + "/api/ws/dashboard/metrics", cm
		}
	}
	const running = `[{"Id":"c1","State":"running"}]`
	return []metricsDrainRoute{
		{"metrics/streaming", metrics(running)},
		{"metrics/empty-host", metrics(`[]`)},
		{"dashboard/streaming", dashboard(running)},
		{"dashboard/empty-host", dashboard(`[]`)},
	}
}

// dialMetricsDrain opens one socket, waits until its slot is counted, then
// reads one frame. A frame is written only from the streaming or empty-host
// loop, so after it the handler is in its loop; a client leaving before that
// exits through the first container lookup instead, which never tests a
// loop's exit. Reading one frame answers no ping (the first is 30s away), so
// the half-open client still never sends a pong.
func dialMetricsDrain(t *testing.T, url string, cm *ConnectionManager) *websocket.Conn {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	resp.Body.Close()
	require.Eventually(t, func() bool { return cm.Count() == 1 },
		time.Until(hangGuardDeadline(t)), 5*time.Millisecond, "the socket never registered")
	require.NoError(t, c.SetReadDeadline(hangGuardDeadline(t)))
	_, _, err = c.ReadMessage()
	require.NoError(t, err, "no first frame: the handler never reached its loop")
	return c
}

// TestMetricsWS_HalfOpenPeerFreesSlot is the regression test for
// agent-os-q48p. The metrics and dashboard sockets had no reader, so their
// only disconnect detector was a failed write. A half-open peer (network gone,
// no FIN or RST) makes no write fail until TCP gives up retransmitting, so its
// per-user slot stayed counted for that long. The client here models it: the
// TCP connection stays up, and it never reads, so it never answers the
// server's ping. With a reader, the 60s read deadline upgradeConnection sets
// (refreshed only by a pong) ends the handler.
//
// The budget is the read deadline plus slack: a latency bound, not a hang
// guard, because the read deadline IS the detection time being pinned.
//
// All four routes wait out the one deadline together, so the test costs ~60s
// rather than four times that. Not t.Parallel subtests: the dashboard fixture
// uses t.Setenv, which Go refuses in a parallel test.
func TestMetricsWS_HalfOpenPeerFreesSlot(t *testing.T) {
	const budget = 60*time.Second + 20*time.Second
	type open struct {
		name  string
		cm    *ConnectionManager
		start time.Time
		freed time.Duration // 0 while still held
	}
	var socks []*open
	for _, r := range metricsDrainRoutes() {
		url, cm := r.open(t)
		start := time.Now()
		// No read, ever: no pong goes back, and nothing the server writes
		// fails, since loopback buffers absorb the frames.
		c := dialMetricsDrain(t, url, cm)
		t.Cleanup(func() { _ = c.Close() })
		socks = append(socks, &open{name: r.name, cm: cm, start: start})
	}
	deadline := time.Now().Add(budget) // wall-clock ok: the bound IS the assertion; the 60s read deadline is the detection time being pinned, 20s slack
	for held := len(socks); held > 0 && time.Now().Before(deadline); {
		held = 0
		for _, s := range socks {
			if s.freed == 0 {
				if s.cm.Count() == 0 {
					s.freed = time.Since(s.start)
				} else {
					held++
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, s := range socks {
		if s.freed == 0 {
			t.Errorf("%s: half-open peer still holds its slot %s after connect: cm.Count()=%d",
				s.name, time.Since(s.start).Round(100*time.Millisecond), s.cm.Count())
			continue
		}
		t.Logf("%s: half-open slot freed after %s", s.name, s.freed.Round(100*time.Millisecond))
	}
}

// TestMetricsWS_ClientGoneFreesSlot pins the abrupt and clean exits through
// the new reader. Before agent-os-q48p they were noticed by the next failed
// write, 2s (streaming) or 4s (empty-host) later. The 1s bound proves watchPeer
// frees the slot, not the next failed write: a loop that ignores ctx.Done()
// still frees it at the next write, inside any hang guard, and only this bound
// tells the two apart.
func TestMetricsWS_ClientGoneFreesSlot(t *testing.T) {
	leaves := []struct {
		name  string
		leave func(t *testing.T, c *websocket.Conn)
	}{
		{"abrupt", func(_ *testing.T, c *websocket.Conn) { _ = c.NetConn().Close() }},
		{"clean", func(t *testing.T, c *websocket.Conn) {
			require.NoError(t, c.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "")))
			_ = c.Close()
		}},
	}
	for _, r := range metricsDrainRoutes() {
		for _, l := range leaves {
			t.Run(r.name+"/"+l.name, func(t *testing.T) {
				url, cm := r.open(t)
				c := dialMetricsDrain(t, url, cm)
				start := time.Now()
				l.leave(t, c)
				ok := waitSlotFreed(t, cm, hangGuardDeadline(t))
				t.Logf("%s/%s: slot freed=%v after %s", r.name, l.name, ok, time.Since(start).Round(time.Millisecond))
				if !ok {
					t.Fatalf("slot never freed after the client left: cm.Count()=%d", cm.Count())
				}
				// wall-clock ok: the bound IS the assertion; watchPeer frees the slot in ms, a failed write takes 2s+
				if took := time.Since(start); took > time.Second {
					t.Errorf("slot freed after %s, want under 1s: the next failed write freed it, not watchPeer", took.Round(time.Millisecond))
				}
			})
		}
	}
}

func waitSlotFreed(t *testing.T, cm *ConnectionManager, deadline time.Time) bool {
	t.Helper()
	for cm.Count() != 0 {
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}
