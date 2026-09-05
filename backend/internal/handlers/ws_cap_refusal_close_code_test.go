package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestMeteredWS_CapRefusalUsesRateLimitCloseCode is the regression for
// agent-os-jj8u: four of the seven metered WebSocket handlers refused a
// per-user connection-cap breach with websocket.CloseNormalClosure (1000).
//
// WHY 1000 IS A DEFECT AND NOT A STYLE CHOICE, and why the harm is unbounded
// rather than a bounded retry burst. frontend/src/lib/ws.ts:16-18 suppresses
// its reconnect ladder for exactly two codes, 4401 and 4429; 1000 reads as an
// ordinary disconnect and reconnects. The ladder looks bounded — maxReconnects
// is 5 at ws.ts:38 — but serveWS UPGRADES FIRST (ws.go: upgradeConnection ->
// upgrader.Upgrade) and only then fails cm.Add and writes the close frame. The
// 101 is already on the wire, so the browser fires onopen, ws.ts:68 resets
// reconnectAttempts to 0, and the close arrives against a counter that has just
// been zeroed. The ladder therefore never advances past attempt 1 and never
// terminates. MEASURED in frontend/src/lib/__tests__/ws-close-policy.test.ts:
// with onopen fired the socket count climbs without limit; with onopen withheld
// the identical close code stops at 6 (1 + 5 retries). The delay is then always
// Math.random() * 2000 (ws.ts:112-115), mean 1s, which is the ~1/second refusal
// storm observed against a live server.
//
// TWO-SIDED BY CONSTRUCTION. The table below carries all SEVEN metered sites,
// not just the four that were wrong. operations and terminal already sent 4429
// before this fix and are marked wasAlreadyCorrect: they are the positive
// control proving this instrument DISCRIMINATES rather than asserting a
// constant — a test that only covered the four broken sites would pass equally
// well against a serveWS that hardcoded 4429 for everything, or against a dial
// helper that silently reported the wanted code.
//
// backup.go is deliberately absent: it registers wsRegistration{unmetered:
// true}, so it has no cap and no refuseCode, and is outside this class.
func TestMeteredWS_CapRefusalUsesRateLimitCloseCode(t *testing.T) {
	cases := []struct {
		name string
		// path is relative to the fixture server's /api group.
		path string
		// wasAlreadyCorrect marks the two positive-control sites.
		wasAlreadyCorrect bool
		newServer         func(t *testing.T, cm *ConnectionManager) *httptest.Server
	}{
		{
			name:      "logs",
			path:      "/api/ws/logs/stack-a",
			newServer: newLogsCapFixture,
		},
		{
			name: "monitoring metrics",
			path: "/api/ws/metrics/metrics-stack",
			newServer: func(t *testing.T, cm *ConnectionManager) *httptest.Server {
				return newMetricsTestFixture(t, nil, cm)
			},
		},
		{
			name: "monitoring events",
			path: "/api/ws/events",
			newServer: func(t *testing.T, cm *ConnectionManager) *httptest.Server {
				return newEventsTestFixture(t, cm, NewEventBus())
			},
		},
		{
			name:      "update jobs",
			path:      "/api/ws/updates/jobs/job-1",
			newServer: newUpdateJobsCapFixture,
		},
		{
			name:      "dashboard metrics",
			path:      "/api/ws/dashboard/metrics",
			newServer: newDashboardCapFixture,
		},
		{
			name:              "operations (positive control)",
			path:              "/api/ws/operations/stack-a/pull",
			wasAlreadyCorrect: true,
			newServer: func(t *testing.T, cm *ConnectionManager) *httptest.Server {
				srv, _ := newOperationsFixture(t, cm)
				return srv
			},
		},
		{
			name:              "terminal (positive control)",
			path:              "/api/ws/terminal/stack-a/proj-a-web-1",
			wasAlreadyCorrect: true,
			newServer: func(t *testing.T, cm *ConnectionManager) *httptest.Server {
				lister := &fakeContainerLister{containersByProject: map[string][]string{"proj-a": {"proj-a-web-1"}}}
				return newTerminalFixture(t, cm, lister).server
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Cap of one, already occupied by the user upgradeConnection
			// assigns under authDisabled -- "anon:"+c.ClientIP(),
			// "anon:127.0.0.1" for this default-dialer httptest client,
			// since agent-os-8uuw -- so the dial below is refused by cm.Add
			// rather than by anything upstream of it.
			cm := NewConnectionManager(1)
			require.NoError(t, cm.Add("already-open", &Connection{ID: "already-open", UserID: "anon:127.0.0.1"}))

			srv := tc.newServer(t, cm)

			code, text := readRefusalCloseCode(t, srv, tc.path)

			if code != CloseCodeRateLimit {
				// The two readings are diagnostically different, so they are
				// reported differently: a control site going red means the
				// shared refusal path or this instrument broke, NOT that
				// jj8u's defect returned at a site that never had it.
				if tc.wasAlreadyCorrect {
					t.Fatalf("POSITIVE CONTROL FAILED: close code = %d (%q), want %d. "+
						"This site already sent 4429 before agent-os-jj8u, so a failure here "+
						"indicts serveWS or this dial helper, not the per-site refuseCode",
						code, text, CloseCodeRateLimit)
				}
				t.Fatalf("close code = %d (%q), want %d (CloseCodeRateLimit) — "+
					"the frontend reconnect ladder (frontend/src/lib/ws.ts:16-18) is suppressed "+
					"only for 4401 and 4429, so any other code leaves a capped client "+
					"refusing and redialling without limit (agent-os-jj8u)", code, text, CloseCodeRateLimit)
			}
		})
	}
}

// readRefusalCloseCode dials a metered WebSocket route and returns the close
// code the server sent, or 0 if it closed without one. Same shape as
// dialOperations/dialTerminal: ONE absolute deadline over the whole read loop,
// because the pass is the close CODE and never its arrival time.
func readRefusalCloseCode(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer conn.Close()
	defer resp.Body.Close()

	closeCode := 0
	closeText := ""
	conn.SetCloseHandler(func(code int, text string) error {
		closeCode, closeText = code, text
		return nil
	})

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ce, ok := err.(*websocket.CloseError); ok && closeCode == 0 {
				closeCode, closeText = ce.Code, ce.Text
			}
			break
		}
	}
	return closeCode, closeText
}

// newLogsCapFixture stands up the logs WebSocket route. StreamLogs gates on a
// non-nil h.docker and an existing stack BEFORE serveWS, so both have to be
// satisfied for the dial to reach the cap check at all — the fake Docker
// endpoint only needs to answer /_ping, since a refusal returns before any
// Docker call is made.
func newLogsCapFixture(t *testing.T, cm *ConnectionManager) *httptest.Server {
	t.Helper()

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	handler := NewLogsHandler(docker, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), cm)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// newUpdateJobsCapFixture stands up the update-jobs WebSocket route. streamJob
// calls serveWS as its very first statement, so a nil UpdateJobManager is
// reachable-safe here: a refused connection returns before the job is ever
// looked up.
func newUpdateJobsCapFixture(t *testing.T, cm *ConnectionManager) *httptest.Server {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewUpdateJobsWSHandler(nil, db, "test-secret-key-32-chars-long!!!", true, cm)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// newDashboardCapFixture stands up the dashboard metrics WebSocket route. Like
// logs, the nil-docker gate runs before serveWS, so h.docker must construct —
// NewDockerService pings at construction — but is never called on a refusal.
func newDashboardCapFixture(t *testing.T, cm *ConnectionManager) *httptest.Server {
	t.Helper()

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewDashboardHandler(nil, docker, db, cm)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}
