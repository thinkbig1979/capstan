package handlers

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// fakeContainerLister answers the compose-project membership lookup without a
// daemon. containersByProject mirrors what
// DockerService.GetContainerList(project) returns: only the containers carrying
// that project's com.docker.compose.project label.
var errDockerLookup = errors.New("cannot connect to the Docker daemon")

type fakeContainerLister struct {
	containersByProject map[string][]string
	err                 error
	calls               int
	// entered is closed on the first lookup and block, when non-nil, holds the
	// handler there. Together they give a test a point where the handler is
	// known to be past registration but not yet finished, which is the only way
	// to observe that the slot was actually occupied.
	entered chan struct{}
	block   chan struct{}
}

func (f *fakeContainerLister) GetContainerList(projectName string) ([]models.Container, error) {
	f.calls++
	if f.entered != nil {
		close(f.entered)
		f.entered = nil
	}
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]models.Container, 0)
	for _, name := range f.containersByProject[projectName] {
		out = append(out, models.Container{ID: "id-" + name, Name: name})
	}
	return out, nil
}

type terminalFixture struct {
	server   *httptest.Server
	db       *database.DB
	terminal *services.TerminalService
	cm       *ConnectionManager
	lister   *fakeContainerLister
}

// newTerminalFixture stands up a real HTTP server so tests dial a genuine
// WebSocket and observe the actual close code, rather than asserting on an
// upgrade that never happened.
//
// Two stacks exist: "stack-a" (project "proj-a", container "proj-a-web-1") and
// "stack-b" (project "proj-b", container "proj-b-db-1").
func newTerminalFixture(t *testing.T, cm *ConnectionManager, docker ContainerLister) *terminalFixture {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	createTestDirectory(t, db, "/test/dir")
	for _, s := range []models.Stack{
		{ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml", ProjectName: "proj-a", Status: "running"},
		{ID: "stack-b", Directory: "/test/dir", ComposeFile: "compose.yaml", ProjectName: "proj-b", Status: "running"},
	} {
		require.NoError(t, db.UpsertStack(s))
	}

	terminal := services.NewTerminalService(&config.Config{})

	// authDisabled=true so upgradeConnection assigns the "anonymous" user
	// without a JWT; the cap and the scoping check are what these tests are
	// about, and both run after authentication either way.
	handler := NewTerminalHandler(terminal, docker, db, cm, services.NewActionLogger(db))

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &terminalFixture{server: srv, db: db, terminal: terminal, cm: cm, lister: nil}
}

// dialTerminal opens the terminal WebSocket and returns the close code the
// server sent, or 0 if the connection closed without one.
func dialTerminal(t *testing.T, f *terminalFixture, stackID, container string) (int, string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/api/ws/terminal/" + stackID + "/" + container
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err, "dialing %s", url)
	defer conn.Close()

	closeCode := 0
	closeText := ""
	conn.SetCloseHandler(func(code int, text string) error {
		closeCode, closeText = code, text
		return nil
	})

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
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

// requireSlotsReleased waits for the handler goroutine to run its deferred
// Remove. The client returns as soon as it sees the close frame, which can be
// before the server side has unwound, so a bare assertion races. Failing on
// timeout still catches a Remove that never runs, which is the thing under test.
func requireSlotsReleased(t *testing.T, cm *ConnectionManager, userID, when string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cm.CountByUser(userID) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s the user still holds %d slot(s); the deferred Remove did not run", when, cm.CountByUser(userID))
}

func deniedRows(t *testing.T, db *database.DB) []models.ActionLog {
	t.Helper()
	rows, _, err := db.ListActionLogsFiltered(50, 0, database.ActionLogFilter{Action: "terminal_denied"})
	require.NoError(t, err)
	return rows
}

// ── agent-os-7u5: container/stack scoping ───────────────────────────────────

// TestTerminalRejectsContainerFromAnotherStack is the core of the issue: stack
// A's ID plus stack B's container. The route signature implies this is scoped;
// before this change the :id parameter was decorative.
//
// The assertion that matters is SessionCount()==0 — rejecting after the fork
// would be no fix at all, since the fork is the `docker exec` process.
func TestTerminalRejectsContainerFromAnotherStack(t *testing.T) {
	lister := &fakeContainerLister{containersByProject: map[string][]string{
		"proj-a": {"proj-a-web-1"},
		"proj-b": {"proj-b-db-1"},
	}}
	f := newTerminalFixture(t, NewConnectionManager(5), lister)

	code, text := dialTerminal(t, f, "stack-a", "proj-b-db-1")

	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d (%q), want %d", code, text, CloseCodeAuthFailure)
	}
	if n := f.terminal.SessionCount(); n != 0 {
		t.Errorf("%d terminal session(s) created; the rejection must happen before any process is spawned", n)
	}

	rows := deniedRows(t, f.db)
	if len(rows) != 1 {
		t.Fatalf("action log has %d terminal_denied rows, want 1", len(rows))
	}
	if rows[0].StackID != "stack-a" {
		t.Errorf("action log stack = %q, want %q", rows[0].StackID, "stack-a")
	}
	if !strings.Contains(rows[0].Detail, "proj-b-db-1") {
		t.Errorf("action log detail %q does not name the requested container", rows[0].Detail)
	}
}

// TestTerminalRejectsContainerNotManagedByCapstan covers a container that
// belongs to no compose project Capstan knows about — the "any container on the
// host" case.
func TestTerminalRejectsContainerNotManagedByCapstan(t *testing.T) {
	lister := &fakeContainerLister{containersByProject: map[string][]string{
		"proj-a": {"proj-a-web-1"},
	}}
	f := newTerminalFixture(t, NewConnectionManager(5), lister)

	code, _ := dialTerminal(t, f, "stack-a", "some-unrelated-host-container")

	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d, want %d", code, CloseCodeAuthFailure)
	}
	if n := f.terminal.SessionCount(); n != 0 {
		t.Errorf("%d session(s) created, want 0", n)
	}
}

// TestTerminalAllowsContainerInStack is the other half: a container that really
// does carry the stack's project label must get past the gate.
//
// It asserts the request was NOT rejected by the scoping check rather than that
// a shell opened — spawning a real `docker exec` needs a live container, and CI
// has neither. Reaching session creation is the behaviour under test.
func TestTerminalAllowsContainerInStack(t *testing.T) {
	lister := &fakeContainerLister{containersByProject: map[string][]string{
		"proj-a": {"proj-a-web-1"},
	}}
	f := newTerminalFixture(t, NewConnectionManager(5), lister)

	code, text := dialTerminal(t, f, "stack-a", "proj-a-web-1")

	if code == CloseCodeAuthFailure {
		t.Fatalf("container in its own stack was rejected by the scoping check: %d %q", code, text)
	}
	if rows := deniedRows(t, f.db); len(rows) != 0 {
		t.Errorf("action log has %d terminal_denied rows for an allowed container, want 0", len(rows))
	}
	if lister.calls != 1 {
		t.Errorf("membership was looked up %d times, want 1", lister.calls)
	}
}

// TestTerminalDeniesWhenDockerUnavailable: membership cannot be verified with no
// daemon, so the check must fail closed rather than fall back to the old
// unchecked behaviour.
func TestTerminalDeniesWhenDockerUnavailable(t *testing.T) {
	f := newTerminalFixture(t, NewConnectionManager(5), nil)

	code, _ := dialTerminal(t, f, "stack-a", "proj-a-web-1")

	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d, want %d — the check must fail closed", code, CloseCodeAuthFailure)
	}
	if n := f.terminal.SessionCount(); n != 0 {
		t.Errorf("%d session(s) created, want 0", n)
	}
}

// TestTerminalDeniesWhenContainerLookupFails — a failed lookup is not proof of
// membership either.
func TestTerminalDeniesWhenContainerLookupFails(t *testing.T) {
	lister := &fakeContainerLister{err: errDockerLookup}
	f := newTerminalFixture(t, NewConnectionManager(5), lister)

	code, _ := dialTerminal(t, f, "stack-a", "proj-a-web-1")

	if code != CloseCodeAuthFailure {
		t.Fatalf("close code = %d, want %d", code, CloseCodeAuthFailure)
	}
}

// ── agent-os-a0y: connection cap ────────────────────────────────────────────

// TestTerminalRefusesBeyondPerUserCap: the cap is what a runaway reconnect loop
// hits. 4429, not a generic disconnect, so the client can say why.
func TestTerminalRefusesBeyondPerUserCap(t *testing.T) {
	cm := NewConnectionManager(1)
	// Occupy the single slot for the user upgradeConnection will assign
	// (authDisabled -> "anonymous").
	require.NoError(t, cm.Add("already-open", &Connection{ID: "already-open", UserID: "anonymous"}))

	lister := &fakeContainerLister{containersByProject: map[string][]string{"proj-a": {"proj-a-web-1"}}}
	f := newTerminalFixture(t, cm, lister)

	code, text := dialTerminal(t, f, "stack-a", "proj-a-web-1")

	if code != CloseCodeRateLimit {
		t.Fatalf("close code = %d (%q), want %d", code, text, CloseCodeRateLimit)
	}
	if n := f.terminal.SessionCount(); n != 0 {
		t.Errorf("%d session(s) created for a refused connection, want 0", n)
	}
	if lister.calls != 0 {
		t.Errorf("refused connection performed %d container lookups, want 0", lister.calls)
	}
}

// TestTerminalOccupiesThenFreesItsSlot proves both halves. Asserting only that
// the count is zero at the end would pass against a handler that never
// registered at all, so the slot is first observed as *held* while the handler
// is parked inside the membership lookup.
func TestTerminalOccupiesThenFreesItsSlot(t *testing.T) {
	cm := NewConnectionManager(5)
	entered := make(chan struct{})
	release := make(chan struct{})
	lister := &fakeContainerLister{
		containersByProject: map[string][]string{"proj-a": {"proj-a-web-1"}},
		entered:             entered,
		block:               release,
	}
	f := newTerminalFixture(t, cm, lister)

	done := make(chan struct{})
	go func() { defer close(done); dialTerminal(t, f, "stack-a", "proj-a-web-1") }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never reached the membership lookup")
	}

	if n := cm.CountByUser("anonymous"); n != 1 {
		t.Errorf("while the connection is live the user holds %d slot(s), want 1 — the connection was never registered", n)
	}

	close(release)
	<-done
	requireSlotsReleased(t, cm, "anonymous", "after the connection ended")
}

// TestTerminalFreesSlotOnAbnormalTermination is the assertion the issue singles
// out: the decrement must run when the connection dies badly, not only on a
// clean close. Here the server-side handler exits via the scoping rejection and
// via session-creation failure — neither is a graceful client close — and the
// slot must still be free afterwards.
func TestTerminalFreesSlotOnAbnormalTermination(t *testing.T) {
	cm := NewConnectionManager(1)
	lister := &fakeContainerLister{containersByProject: map[string][]string{
		"proj-a": {"proj-a-web-1"},
		"proj-b": {"proj-b-db-1"},
	}}
	f := newTerminalFixture(t, cm, lister)

	// Exit path 1: rejected by the scoping check.
	dialTerminal(t, f, "stack-a", "proj-b-db-1")
	requireSlotsReleased(t, cm, "anonymous", "after a rejected connection")

	// Exit path 2: allowed through, then the session ends (no daemon in test, or
	// the PTY exits immediately). Either way the handler returns abnormally.
	dialTerminal(t, f, "stack-a", "proj-a-web-1")
	requireSlotsReleased(t, cm, "anonymous", "after an abnormally terminated connection")

	// The freed slot is genuinely reusable: with a cap of 1, a third connection
	// must not be refused.
	code, _ := dialTerminal(t, f, "stack-a", "proj-a-web-1")
	if code == CloseCodeRateLimit {
		t.Error("slot was not released: a later connection was refused with the rate-limit code")
	}
}

// The host-wide ceiling itself is covered in
// internal/services/terminal_test.go, where the limit field is reachable.
