package handlers

import (
	"context"
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
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// fakeStreamer stands in for DockerService without a daemon. It emits one line
// and a terminal done frame, so the handler runs its full streaming path and
// then returns, which is what the slot-release assertions need.
type fakeStreamer struct {
	block chan struct{}
}

func (f *fakeStreamer) RunStreaming(ctx context.Context, stack models.Stack, subcommand string, extraArgs []string) <-chan services.StreamLine {
	out := make(chan services.StreamLine, 4)
	go func() {
		defer close(out)
		if f.block != nil {
			select {
			case <-f.block:
			case <-ctx.Done():
				return
			}
		}
		out <- services.StreamLine{Type: "output", Line: subcommand + " running"}
		out <- services.StreamLine{Type: "done", Success: true}
	}()
	return out
}

// newOperationsFixture stands up a real server for the operations WebSocket, so
// tests dial a genuine connection and read the actual close code.
func newOperationsFixture(t *testing.T, cm *ConnectionManager) (*httptest.Server, *database.DB) {
	return newOperationsFixtureWith(t, cm, &fakeStreamer{})
}

func newOperationsFixtureWith(t *testing.T, cm *ConnectionManager, streamer OperationStreamer) (*httptest.Server, *database.DB) {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	handler := NewOperationsHandler(streamer, db, services.NewOperationLock(), cm)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, db
}

func dialOperations(t *testing.T, srv *httptest.Server, stackID, action string) (int, string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/operations/" + stackID + "/" + action
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

	// Hang guard, and the bound here is subtler than it looks: this is ONE
	// ABSOLUTE deadline covering the ENTIRE loop below, not a per-read one.
	// The 5s it replaces was therefore a budget on the CALLER's progress, not
	// on any single read — TestOperationsOccupiesThenFreesItsSlot runs this
	// loop in a goroutine while the test body polls for registration and only
	// then closes `release`, so a slow box could expire this deadline before
	// the stream it is reading had been allowed to start. The loop's exit is
	// the server closing; the PASS is the close CODE, never its arrival time.
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

// TestOperationsRefusesBeyondPerUserCap — operations streams were the other
// endpoint missing from the ConnectionManager the four other WebSocket handlers
// already use (agent-os-a0y).
func TestOperationsRefusesBeyondPerUserCap(t *testing.T) {
	cm := NewConnectionManager(1)
	// "anon:127.0.0.1", not the literal "anonymous": since agent-os-8uuw,
	// upgradeConnection assigns "anon:"+c.ClientIP() under AUTH_DISABLED, and
	// the dial below is unauthenticated default-dialer traffic from
	// 127.0.0.1, so this is the identity that actually collides with it.
	require.NoError(t, cm.Add("already-open", &Connection{ID: "already-open", UserID: "anon:127.0.0.1"}))

	srv, _ := newOperationsFixture(t, cm)

	code, text := dialOperations(t, srv, "stack-a", "pull")

	if code != CloseCodeRateLimit {
		t.Fatalf("close code = %d (%q), want %d", code, text, CloseCodeRateLimit)
	}
}

// TestOperationsOccupiesThenFreesItsSlot proves both halves: a final count of
// zero would also hold for a handler that never registered, so the slot is first
// observed as held while the stream is parked.
func TestOperationsOccupiesThenFreesItsSlot(t *testing.T) {
	cm := NewConnectionManager(5)
	release := make(chan struct{})
	srv, _ := newOperationsFixtureWith(t, cm, &fakeStreamer{block: release})

	done := make(chan struct{})
	go func() { defer close(done); dialOperations(t, srv, "stack-a", "pull") }()

	// Hang guard on a durable observable: fakeStreamer{block: release} parks
	// the handler inside RunStreaming, so once the connection is registered it
	// STAYS registered until this test itself closes `release` below. That is
	// the precondition firstConnection's doc comment names (agent-os-gs7r) —
	// a poll cannot catch a registration that has already been removed, and
	// widening the bound would not save it. Here the test owns the release, so
	// the wait is sound and the bound is only the "it never registered" answer.
	// "anon:127.0.0.1", not the literal "anonymous": since agent-os-8uuw,
	// upgradeConnection assigns "anon:"+c.ClientIP() under AUTH_DISABLED, and
	// dialOperations is unauthenticated default-dialer traffic from 127.0.0.1.
	guard := hangGuardDeadline(t)
	for cm.CountByUser("anon:127.0.0.1") == 0 && time.Now().Before(guard) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := cm.CountByUser("anon:127.0.0.1"); n != 1 {
		t.Errorf("while the stream is live the user holds %d slot(s), want 1 — the connection was never registered", n)
	}

	close(release)
	<-done
	requireSlotsReleased(t, cm, "anon:127.0.0.1", "after the stream ended")
}

// TestOperationsFreesSlotOnAbnormalTermination — the decrement has to run when
// the stream dies badly, not only on a clean close.
func TestOperationsFreesSlotOnAbnormalTermination(t *testing.T) {
	cm := NewConnectionManager(1)
	srv, _ := newOperationsFixture(t, cm)

	dialOperations(t, srv, "stack-a", "pull")
	requireSlotsReleased(t, cm, "anon:127.0.0.1", "after an abnormally terminated operations stream")

	// With a cap of 1, a second connection proves the slot is genuinely reusable.
	code, _ := dialOperations(t, srv, "stack-a", "pull")
	if code == CloseCodeRateLimit {
		t.Error("slot was not released: a later connection was refused with the rate-limit code")
	}
}

// TestOperationsRefusesWhenDockerUnavailable is the regression test for a
// process-killing panic found while writing the cap tests above: with the daemon
// unreachable at startup main leaves dockerService nil, and RunStreaming
// dereferenced it inside a goroutine —
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	services.(*DockerService).buildComposeArgs(0x0, ...)  docker.go:241
//	services.(*DockerService).RunStreaming.func1()        docker_lifecycle.go:536
//
// A panic in a goroutine is not caught by RecoveryMiddleware, so this took the
// whole server down rather than failing one request. The wider sweep of
// nil-docker paths is agent-os-xay.
func TestOperationsRefusesWhenDockerUnavailable(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	handler := NewOperationsHandler(nil, db, services.NewOperationLock(), NewConnectionManager(5))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/operations/stack-a/pull"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		conn.Close()
		t.Fatal("expected the upgrade to be refused with Docker unavailable")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

// TestOperationsRegistersOnlyAfterAuthentication: the cap must key on a real
// user, so registration happens after upgradeConnection has authenticated. An
// unknown action is rejected before the upgrade, so nothing is registered.
func TestOperationsRejectsUnknownActionWithoutConsumingASlot(t *testing.T) {
	cm := NewConnectionManager(1)
	srv, _ := newOperationsFixture(t, cm)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/operations/stack-a/bogus"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		conn.Close()
		t.Fatal("expected the upgrade to be refused for an unknown action")
	}
	if resp != nil && resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if n := cm.Count(); n != 0 {
		t.Errorf("%d connection(s) registered for a request rejected before the upgrade, want 0", n)
	}
}
