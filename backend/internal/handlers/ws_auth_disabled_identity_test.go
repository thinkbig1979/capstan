package handlers

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// wsIdentityRaceResult is what either half of
// waitForDistinctIdentityOrRefusal reports: either a SECOND connection
// registered under a UserID different from the first (the fix), or the
// second connection's socket received a cap-refusal close frame (the
// pre-fix defect: both connections shared one UserID and the shared cap of
// 1 refused the second).
type wsIdentityRaceResult struct {
	registered bool // true: "registered" branch won; false: "refused" branch won
	userID     string
	closeCode  int
}

// waitForDistinctIdentityOrRefusal races two observations of conn2/cm against
// each other and returns whichever resolves first, bounded by t's hang-guard
// deadline (hangGuardDeadline, backup_ws_cap_test.go). This does NOT assume
// which identity string the fix will use -- it discovers client 1's actual
// UserID from cm (via firstConnection, monitoring_metrics_close_test.go) and
// then asks only "did a second, DIFFERENT bucket appear, or did the second
// socket get refused" -- so it is a fair, symmetric instrument that reports
// a real answer quickly in EITHER the broken or the fixed state, rather than
// paying the full hang-guard ceiling to prove an absence, in either direction.
func waitForDistinctIdentityOrRefusal(t *testing.T, cm *ConnectionManager, firstUserID string, conn2 *websocket.Conn) wsIdentityRaceResult {
	t.Helper()

	resultCh := make(chan wsIdentityRaceResult, 2)
	guard := hangGuardDeadline(t)

	// Both watchers below self-terminate at guard, so closing resultCh once
	// both have returned turns "neither watcher resolved" into a CHANNEL
	// CLOSE rather than a timer expiry. There is therefore no wall-clock
	// bound in this function at all: the read below resolves on the actual
	// event, and its failure arm resolves on the watchers actually giving up
	// (agent-os-jar5). It replaces `time.After(time.Until(guard) + 500ms)`,
	// where the additive slop existed only to order the outer timer after the
	// watchers -- an ordering a WaitGroup states instead of estimating.
	var watchers sync.WaitGroup
	watchers.Add(2)
	go func() {
		watchers.Wait()
		close(resultCh)
	}()

	// Watcher A: poll cm for a connection registered under a UserID
	// different from firstUserID -- the fixed-code outcome.
	go func() {
		defer watchers.Done()
		for {
			cm.mu.RLock()
			for _, c := range cm.connections {
				if c.UserID != firstUserID {
					cm.mu.RUnlock()
					resultCh <- wsIdentityRaceResult{registered: true, userID: c.UserID}
					return
				}
			}
			cm.mu.RUnlock()
			if !time.Now().Before(guard) {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// Watcher B: read conn2's close frame -- the pre-fix outcome (both
	// connections shared firstUserID, so cm.Add refused the second at cap 1).
	go func() {
		defer watchers.Done()
		_ = conn2.SetReadDeadline(guard)
		for {
			if _, _, err := conn2.ReadMessage(); err != nil {
				if ce, ok := err.(*websocket.CloseError); ok {
					resultCh <- wsIdentityRaceResult{registered: false, closeCode: ce.Code}
				}
				return
			}
		}
	}()

	r, resolved := <-resultCh
	if !resolved {
		t.Fatal("neither watcher resolved: conn2 was neither registered under a distinct " +
			"identity nor refused with a close frame before the hang-guard deadline")
	}
	return r
}

// TestAuthDisabledWS_DistinctClientIdentitiesGetIndependentCapBuckets is the
// regression test for agent-os-8uuw: under AUTH_DISABLED, upgradeConnection
// assigned every connection the literal UserID "anonymous" (ws.go:521 before
// this fix), so ConnectionManager.Add's per-user cap (ws.go:133) became one
// server-wide bucket shared across all six metered routes, for every client
// on the host.
//
// TEST-DESIGN HAZARD (named in the bead brief so it isn't rediscovered):
// every httptest dial originates from 127.0.0.1. Two plain
// websocket.DefaultDialer dials therefore share one identity BY
// CONSTRUCTION regardless of the fix, so the second dial below binds its
// local address to a SECOND loopback IP (127.0.0.2, via net.Dialer.LocalAddr)
// to produce two connections that are genuinely different peers, the same
// way two different real machines would be. 127.0.0.0/8 is entirely loopback
// on Linux, so 127.0.0.2 needs no extra host configuration.
//
// TWO metered routes, not one, sharing ONE ConnectionManager: proves the
// collapse (and the fix) crosses route boundaries, not just within a single
// handler. Reuses the unmodified newLogsCapFixture/newDashboardCapFixture
// from ws_cap_refusal_close_code_test.go (agent-os-jj8u) rather than adding a
// third pair of fixtures.
func TestAuthDisabledWS_DistinctClientIdentitiesGetIndependentCapBuckets(t *testing.T) {
	cm := NewConnectionManager(1) // cap 1: the whole point is one slot per identity

	logsSrv := newLogsCapFixture(t, cm)
	dashSrv := newDashboardCapFixture(t, cm)

	// Client 1: default dialer, effectively 127.0.0.1, on the logs route.
	conn1, resp1, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(logsSrv.URL, "http")+"/api/ws/logs/stack-a", nil)
	require.NoError(t, err, "dialing client 1 (logs)")
	defer conn1.Close()
	defer resp1.Body.Close()

	// firstConnection (monitoring_metrics_close_test.go) discovers whatever
	// UserID the current code actually assigned, rather than this test
	// assuming its own fix's output format -- so it is a fair oracle in both
	// the pre-fix and post-fix state.
	firstConn := firstConnection(t, cm)
	firstUserID := firstConn.UserID

	// Client 2: dialer bound to a SECOND loopback address, on the dashboard
	// route. serveWS upgrades before it ever calls cm.Add (documented at
	// ws.go:669-682 and in ws_cap_refusal_close_code_test.go's header
	// comment), so this Dial succeeds regardless of whether the connection is
	// about to be refused -- the refusal shows up only in what happens next.
	dialer2 := &websocket.Dialer{
		NetDial: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}).Dial,
	}
	conn2, resp2, err := dialer2.Dial(
		"ws"+strings.TrimPrefix(dashSrv.URL, "http")+"/api/ws/dashboard/metrics", nil)
	require.NoError(t, err, "dialing client 2 (dashboard) -- upgrade always succeeds before the cap check runs")
	defer conn2.Close()
	defer resp2.Body.Close()

	// PRE-FIX: both clients land in the SAME UserID ("anonymous"), so cm.Add
	// refuses the second at cap 1 -- watcher B wins fast, reporting a
	// CloseCodeRateLimit close frame, and this assertion fails, which is the
	// "seen failing first" evidence for this bead.
	// POST-FIX: the two clients are distinct identities, each with their own
	// slot -- watcher A wins fast, reporting a second, different UserID.
	result := waitForDistinctIdentityOrRefusal(t, cm, firstUserID, conn2)
	require.Truef(t, result.registered,
		"client 2 was refused with close code %d instead of registering under a distinct identity "+
			"(client 1's UserID was %q) -- the per-user cap collapsed both clients into one bucket",
		result.closeCode, firstUserID)
	require.NotEqual(t, firstUserID, result.userID, "client 2 must not share client 1's identity")

	// Both slots are independently accounted: two identities, cap 1 each,
	// both consumed, nobody refused.
	require.Equal(t, 1, cm.CountByUser(firstUserID))
	require.Equal(t, 1, cm.CountByUser(result.userID))
}

// TestAuthenticatedWS_DifferentUsersGetIndependentCapBuckets is the CONTROL
// for the ARM above (agent-os-8uuw). With AUTH_DISABLED false, two really
// different, JWT-authenticated user identities on two different metered
// routes must NOT share a bucket -- proving this test's instrument
// (requireConnRegistered by UserID, one ConnectionManager shared across two
// route fixtures) discriminates real bucketing rather than reporting
// "not refused" no matter what identity scheme is in play. This code path is
// untouched by the fix (real auth already assigned distinct real user IDs);
// it is run to show the fix didn't have to break it, and didn't.
func TestAuthenticatedWS_DifferentUsersGetIndependentCapBuckets(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!!"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	userA, tokenA := createAuthedTestUser(t, db, secret)
	userB, tokenB := createAuthedTestUser(t, db, secret)

	cm := NewConnectionManager(1)

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))

	logsHandler := NewLogsHandler(docker, db, secret, false, t.TempDir(), cm)
	logsRouter := gin.New()
	logsHandler.RegisterRoutes(logsRouter.Group("/api"))
	logsSrv := httptest.NewServer(logsRouter)
	t.Cleanup(logsSrv.Close)

	dashHandler := NewDashboardHandler(nil, docker, db, cm)
	dashRouter := gin.New()
	dashHandler.RegisterRoutes(dashRouter.Group("/api"), secret, false)
	dashSrv := httptest.NewServer(dashRouter)
	t.Cleanup(dashSrv.Close)

	connA, respA, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(logsSrv.URL, "http")+"/api/ws/logs/stack-a",
		http.Header{"Cookie": []string{"capstan_token=" + tokenA}})
	require.NoError(t, err, "dialing user A (logs)")
	defer connA.Close()
	defer respA.Body.Close()
	requireConnRegistered(t, cm, func(c *Connection) bool { return c.UserID == userA }, "user A (logs)")

	connB, respB, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(dashSrv.URL, "http")+"/api/ws/dashboard/metrics",
		http.Header{"Cookie": []string{"capstan_token=" + tokenB}})
	require.NoError(t, err, "dialing user B (dashboard)")
	defer connB.Close()
	defer respB.Body.Close()
	requireConnRegistered(t, cm, func(c *Connection) bool { return c.UserID == userB }, "user B (dashboard)")

	require.Equal(t, 1, cm.CountByUser(userA))
	require.Equal(t, 1, cm.CountByUser(userB))
}

// createAuthedTestUser creates a user and a live session row, returning the
// user's ID and a JWT cookie token authenticateToken (ws.go) will accept --
// same shape as TestAuthenticateToken_ValidToken (ws_test.go).
func createAuthedTestUser(t *testing.T, db *database.DB, secret string) (userID, token string) {
	t.Helper()

	userID = uuid.New().String()
	require.NoError(t, db.CreateUser(models.User{
		ID:        userID,
		Username:  "user-" + userID[:8],
		Password:  "hashedpassword",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}))

	sessionID := uuid.New().String()
	require.NoError(t, db.CreateSession(models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}))

	tok, err := generateJWT(userID, "user-"+userID[:8], sessionID, secret)
	require.NoError(t, err)
	return userID, tok
}
