package handlers

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// newBackupWSFixtureWithHandler is newBackupWSFixtureWithLogger plus the
// handler itself, whose registry the test below drives directly to fill the
// per-run attacher bound deterministically (see the test's doc comment for
// why it does not dial the bound over WS).
func newBackupWSFixtureWithHandler(t *testing.T, cm *ConnectionManager, release chan struct{}, logger *slog.Logger) (*httptest.Server, *BackupHandler) {
	t.Helper()

	db := newBackupHandlerDB(t)
	svc := buildBlockingBackupSvc(t, db, release)
	h := NewBackupHandler(svc, db, logger)
	t.Cleanup(h.Stop)
	h.SetConnectionManager(cm)

	router := newBackupRouter(h)
	wsGroup := router.Group("/api")
	h.RegisterWSRoutes(wsGroup, "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, h
}

// readUntilTerminal reads frames until one whose type is "refused" or "done"
// arrives, and returns it. Data/phase/start frames on the way are skipped.
func readUntilTerminal(t *testing.T, conn *websocket.Conn, what string) map[string]interface{} {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	for {
		var frame map[string]interface{}
		require.NoError(t, conn.ReadJSON(&frame), "%s: reading frames until a terminal one", what)
		switch frame["type"] {
		case "refused", "done":
			return frame
		}
	}
}

// TestBackupWSAttach_SurplusViewerGetsRefusedFrameNotDone pins agent-os-mjrl
// at the handler: a viewer turned away at the per-run attacher bound
// (services.BackupRunnerRegistry.Attach, agent-os-nt0m) receives a DISTINCT
// {"type":"refused","reason":...} frame and then the socket closes. It never
// receives a done frame, because a refused attach is not a run completion and
// the done frame's outcome vocabulary is the run's. Before this change the
// refusal was delivered BY RESULT as a done frame with outcome "failed", which
// the frontend rendered as "Restore failed" for a run that was still fine.
//
// The bound is filled by calling the registry's Attach directly, once per
// slot, until it refuses: that is synchronous and deterministic, whereas
// dialing the bound over WS gives the test nothing to wait on (the start
// frame is written BEFORE the handler's Attach, so reading it proves nothing
// about the slot, and a blocked run replays no line) and would have to
// hard-code the constant besides. The handler-level contract under test is
// the frame shape for a refusal, not the arithmetic of the bound, which
// TestAttach_BoundsAttachersPerRun already pins in services.
//
// Two-sided on the SAME instrument (readUntilTerminal on a dialed client):
// after one direct attacher leaves and its slot is released, a fresh WS
// client is admitted, sees no refused frame, and on the run's genuine
// completion receives the real done frame with a non-empty outcome. That is
// what stops "never send a done frame" from looking like a fix.
func TestBackupWSAttach_SurplusViewerGetsRefusedFrameNotDone(t *testing.T) {
	cm := NewConnectionManager(10)
	release := make(chan struct{})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	srv, h := newBackupWSFixtureWithHandler(t, cm, release, logger)
	releaseOnce := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	// Registered AFTER the fixture so LIFO unblocks h.Stop's cleanup first.
	t.Cleanup(releaseOnce)

	runID := kickOffBackupRun(t, srv)

	// --- Fill the bound directly. Each admitted Attach holds a slot until its
	// clientGone closes; the first one is released below for the control arm.
	const attachCeiling = 256 // far above any plausible bound; a guard, not a budget
	var gone []chan struct{}
	var lives []<-chan services.StreamLine
	var refusal *services.AttachResult
	for i := 0; i < attachCeiling && refusal == nil; i++ {
		g := make(chan struct{})
		res, err := h.registry.Attach(runID, g)
		require.NoError(t, err, "direct attach %d", i+1)
		if res.Refused {
			close(g)
			refusal = res
			break
		}
		require.False(t, res.Done, "direct attach %d: the run is blocked, it cannot be done", i+1)
		gone = append(gone, g)
		lives = append(lives, res.Live)
	}
	for _, g := range gone[1:] {
		defer close(g)
	}
	require.NotNil(t, refusal, "the registry never refused within %d direct attaches; is the bound gone?", attachCeiling)
	require.NotEmpty(t, refusal.Reason)

	// --- REFUSED: the next WS viewer is the surplus one.
	surplus := dialBackupWS(t, srv, "/api/ws/backups/run/"+runID, "")
	defer surplus.Close()
	requireBackupStartFrame(t, surplus)
	frame := readUntilTerminal(t, surplus, "surplus viewer")
	require.Equal(t, "refused", frame["type"],
		"a surplus viewer must get a refused frame, not a done frame; got %v", frame)
	require.Equal(t, refusal.Reason, frame["reason"],
		"the refused frame carries the registry's reason (it names the limit)")
	_, hasOutcome := frame["outcome"]
	require.False(t, hasOutcome, "a refused frame is not a done frame and carries no run outcome; got %v", frame)

	// After the refused frame the handler returns and the connection closes:
	// the next read must fail, and it must not be a done frame.
	require.NoError(t, surplus.SetReadDeadline(hangGuardDeadline(t)))
	var after map[string]interface{}
	err := surplus.ReadJSON(&after)
	require.Error(t, err, "the refused viewer's socket must close after the refused frame; instead read %v", after)

	// sendDoneFrame was never called for the refusal: its log line would name
	// this run with the completion message. Read before any completion so the
	// only possible source of that line is the refusal path. Reading buf only
	// after the surplus connection has left cm is what makes the read
	// race-free (same discipline as the b53l test): release() deregisters
	// under cm's lock as the last thing wsAttach's defer stack runs, after
	// every synchronous log call on that path.
	waitForCMCount(t, cm, 0, "surplus viewer deregistered")
	marker := fmt.Sprintf(`msg="Backup WS operation completed" run_id=%s`, runID)
	require.False(t, strings.Contains(buf.String(), marker),
		"a refusal must not log (or send) a completion; captured: %q", buf.String())

	// --- CONTROL: free one slot, a fresh viewer is admitted and gets the real
	// done frame on genuine completion.
	close(gone[0])
	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, ok := <-lives[0]:
			open = ok
		case <-deadline:
			t.Fatal("CONTROL: forwardLive did not exit within 5s of its client going away")
		}
	}

	admitted := dialBackupWS(t, srv, "/api/ws/backups/run/"+runID, "")
	defer admitted.Close()
	requireBackupStartFrame(t, admitted)
	requireConnRegistered(t, cm, func(c *Connection) bool { return true }, "admitted viewer")

	// Let the run finish for real with the admitted viewer attached.
	releaseOnce()
	done := readUntilTerminal(t, admitted, "admitted viewer")
	require.Equal(t, "done", done["type"], "an admitted viewer must get the done frame, got %v", done)
	outcome, _ := done["outcome"].(string)
	require.NotEmpty(t, outcome, "the admitted viewer's done frame must carry the real outcome; got %v", done)
}
