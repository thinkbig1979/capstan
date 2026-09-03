package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Regression tests for agent-os-pu4y: wsAttach (backup.go) registered with a
// SOFT Add, so a backup stream opened while the caller was already at the
// shared per-user cap went unregistered (unrevocable), and — the other
// direction — an under-cap backup stream still spent a slot that the other
// five WS handlers sharing the manager enforce hard.
//
// newBackupWSFixture/dialBackupWS/kickOffBackupRun are new; newOperationsFixtureWith
// and dialOperations are reused from operations_test.go (same package),
// deliberately, so arm 3 below exercises a REAL hard-refusing handler sharing
// the SAME ConnectionManager, not a stand-in.

// blockingCommandRunner is a services.CommandRunner whose every call blocks
// until release is closed (or ctx is cancelled). Injected via
// BackupService.SetResticMgrFactory so a durable run started against it stays
// genuinely "running" — never reaching done — for the whole test body. Without
// this, a real backup with zero stacks finishes in well under a millisecond
// (confirmed: it beat the test to the punch on the first attempt at this file,
// closing the WS itself via defer conn.Conn.Close() before CloseForSession was
// ever called, which manifested as an unrelated-looking 1006 instead of the
// intended 0), making the whole test a race against the run's own completion
// rather than a test of revocation.
type blockingCommandRunner struct {
	release chan struct{}
}

func (r *blockingCommandRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- services.StreamLine) error {
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *blockingCommandRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	select {
	case <-r.release:
		return []byte("{}"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// buildBlockingBackupSvc builds a BackupService whose ResticManager never
// actually execs a binary — every call blocks on release — via the
// SetResticMgrFactory seam (backup.go documents it as intended for exactly
// this: external test packages injecting fake runners).
func buildBlockingBackupSvc(t *testing.T, db *database.DB, release chan struct{}) *services.BackupService {
	t.Helper()

	cfg := &config.Config{
		DataDir:        t.TempDir(),
		StacksDir:      "/opt/stacks",
		AuthDisabled:   true,
		JWTSecret:      "test-secret-32-chars-padding-here",
		ResticPassword: "test-restic-password", // resolveBackupConfig falls back to this; withPasswordFile refuses an empty one
	}

	svc := services.NewBackupService(cfg, db, &noopDocker{}, services.NewOperationLock(), services.NewActionLogger(db))
	svc.SetBins("/usr/bin/restic", "") // non-empty so RunBackup's availability check passes; never actually exec'd
	runner := &blockingCommandRunner{release: release}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, slog.Default())
	})
	return svc
}

// newBackupWSFixture wires a BackupHandler with both its REST routes (needed
// to kick off a durable run) and its WS routes onto one real HTTP server,
// sharing cm the way cmd/server/main.go does. The backup engine is the
// blocking one above: release must be closed by the caller (after the fixture
// call, so t.Cleanup's LIFO order unblocks the run BEFORE h.Stop() waits on
// it) or h.Stop() hangs forever.
//
// When authDisabled is false, the WS route group runs behind the REAL
// middleware.AuthMiddleware, matching cmd/server/main.go's wsGroup (a child of
// `protected`, which has AuthMiddleware). This is required, not decorative:
// upgradeConnection sources Connection.SessionID from `c.GetString("jti")`
// (ws.go), which only AuthMiddleware publishes on the gin context — wsAttach's
// own cookie re-validation never sets it. Without this middleware every test
// connection's SessionID is "", and CloseForSession("") is a deliberate no-op
// (agent-os-teop), which would make a revocation test pass or fail for the
// wrong reason regardless of wsAttach's registration behaviour. The REST
// routes stay on the bare (unauthenticated) group, matching every other test
// in backup_test.go — kickOffBackupRun does not carry a token.
func newBackupWSFixture(t *testing.T, cm *ConnectionManager, authDisabled bool, secret string, release chan struct{}) (*httptest.Server, *database.DB) {
	t.Helper()

	db := newBackupHandlerDB(t)
	svc := buildBlockingBackupSvc(t, db, release)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() must run before db.Close()/t.TempDir() cleanup — see the
	// agent-os-80n comment on the kickoff tests in backup_test.go. It also
	// must run AFTER release is closed (see the caller-side ordering note
	// above), which is why this file never closes release itself.
	t.Cleanup(h.Stop)
	h.SetConnectionManager(cm)

	router := newBackupRouter(h)
	wsGroup := router.Group("/api")
	if !authDisabled {
		wsGroup.Use(middleware.AuthMiddleware(db, secret, authDisabled, ""))
	}
	h.RegisterWSRoutes(wsGroup, secret, authDisabled)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, db
}

// kickOffBackupRun starts a durable backup run (against the blocking engine
// above, so it never reaches "done" during the test) and returns its runID.
func kickOffBackupRun(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	resp, err := http.Post(srv.URL+"/api/backups/run", "application/json",
		strings.NewReader(`{"stackIds":[],"dryRun":false}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	runID, _ := body["runId"].(string)
	require.NotEmpty(t, runID, "runId must be present")
	return runID
}

// dialBackupWS dials a backup WS route (optionally cookie-authenticated) and
// reads past the {"type":"start",...} frame before returning. That frame is
// written AFTER wsAttach's registration step (Add/AddUnmetered), so seeing it
// is proof registration has already happened — the same role
// requireSlotsReleased/`entered` channels play in terminal_scope_test.go.
func dialBackupWS(t *testing.T, srv *httptest.Server, path, cookieToken string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	header := http.Header{}
	if cookieToken != "" {
		header.Set("Cookie", "capstan_token="+cookieToken)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	require.NoError(t, err, "dialing %s", wsURL)
	if resp != nil {
		resp.Body.Close()
	}

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	var startFrame map[string]interface{}
	require.NoError(t, conn.ReadJSON(&startFrame), "reading the start frame")
	require.Equal(t, "start", startFrame["type"])
	return conn
}

// TestBackupWSAttach_RevokesConnectionEvenAtCap is arms 1 and 2 together: a
// backup stream opened while the caller is ALREADY at the per-user cap must
// still be closed by CloseForSession (arm 1, the core defect) — a fix that
// closes indiscriminately is equally wrong, so a different, non-revoked
// session's connection on the same manager must stay open (arm 2).
//
// Seen failing first against pre-fix code: wsAttach's soft
// `if err := h.cm.Add(...); err == nil { defer h.cm.Remove(...) }` never
// registers the backup connection when the cap is already full, so
// cm.CloseForSession(revokedJTI) has nothing to close. The read below then
// blocks to its deadline and closeCode stays 0, failing
// `closeCode == CloseCodeAuthFailure` (0 != 4401).
func TestBackupWSAttach_RevokesConnectionEvenAtCap(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!!"
	const revokedJTI = "backup-ws-revoked-session"

	cm := NewConnectionManager(1) // cap 1, so the control connection alone fills it
	release := make(chan struct{})
	srv, db := newBackupWSFixture(t, cm, false, secret, release)
	t.Cleanup(func() { close(release) }) // registered after the fixture -> runs before its h.Stop cleanup (LIFO)

	user := createTestUser(t, db, "backupwsuser", "password123")
	require.NoError(t, db.CreateSession(models.Session{
		ID:        revokedJTI,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}))
	token := generateTestToken(user.ID, user.Username, revokedJTI, secret)

	// Saturate the user's single slot with an unrelated, NOT-revoked
	// connection BEFORE the backup stream ever attaches — exactly the state
	// the bug describes: a backup viewer opened by a caller already at cap.
	control := &Connection{ID: "control-conn", UserID: user.ID, SessionID: "some-other-live-session"}
	require.NoError(t, cm.Add(control.ID, control))

	runID := kickOffBackupRun(t, srv)
	conn := dialBackupWS(t, srv, "/api/ws/backups/run/"+runID, token)
	defer conn.Close()

	closeCode := 0
	conn.SetCloseHandler(func(code int, text string) error {
		closeCode = code
		return nil
	})
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))

	// The exact call a real logout/password-change makes (see
	// auth_logout_revocation_test.go / settings_password_revocation_test.go).
	cm.CloseForSession(revokedJTI)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ce, ok := err.(*websocket.CloseError); ok && closeCode == 0 {
				closeCode = ce.Code
			}
			break
		}
	}

	require.Equal(t, CloseCodeAuthFailure, closeCode,
		"a backup stream opened while the caller was already at the per-user cap must still be revocable by CloseForSession")

	_, present := cm.Get(control.ID)
	require.True(t, present, "a connection for a different, non-revoked session must stay open — a close-everything fix would also pass arm 1 alone")
}

// TestBackupWSAttach_DoesNotConsumeSharedCapBudget is arm 3: wsAttach must not
// spend a slot in the shared cap-10 manager that the five hard-refusing
// handlers (logs, monitoring, dashboard, operations, update-jobs) enforce.
//
// Seen failing first against pre-fix code: wsAttach's soft Add still SUCCEEDS
// (and increments userCounts) when under cap, so it occupies the single slot
// this test's cap-1 manager allows. The operations WS dial for the same user
// then hits the cap and is refused with CloseCodeRateLimit, failing
// `code != CloseCodeRateLimit`.
func TestBackupWSAttach_DoesNotConsumeSharedCapBudget(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!!"

	cm := NewConnectionManager(1) // cap 1: the whole point is there is only one slot to fight over
	release := make(chan struct{})
	srv, _ := newBackupWSFixture(t, cm, true, secret, release)
	t.Cleanup(func() { close(release) })

	runID := kickOffBackupRun(t, srv)
	conn := dialBackupWS(t, srv, "/api/ws/backups/run/"+runID, "") // authDisabled -> userID "anonymous"
	defer conn.Close()

	// Same manager, same user ("anonymous" under authDisabled), a handler that
	// DOES hard-refuse at the cap (operations_test.go's own fixture/helpers).
	opSrv, _ := newOperationsFixtureWith(t, cm, &fakeStreamer{})
	code, text := dialOperations(t, opSrv, "stack-a", "start")

	require.NotEqual(t, CloseCodeRateLimit, code,
		"operations WS refused (%d %q) — the backup stream consumed a cap slot a hard-refusing handler needed", code, text)
}
