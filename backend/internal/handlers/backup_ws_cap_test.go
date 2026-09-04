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
// returns as soon as the handshake completes. It deliberately reads NOTHING.
//
// It used to read past the {"type":"start",...} frame under a 5-second
// SetReadDeadline, using that frame as a proxy for "registration has already
// happened". That made the frame's ARRIVAL TIME the assertion: on a loaded
// runner the frame missed the budget and a correct handler went red —
// OBSERVED in CI on PR #256 @ 8aed205, where the plain unit job failed with
// `read tcp 127.0.0.1:55412->127.0.0.1:39467: i/o timeout` / "reading the
// start frame" and a rerun of the IDENTICAL SHA passed, while the SLOWER
// race-detector job passed both times (agent-os-fzqb). A failure that appears
// only in the faster job is pointing at load, not at code.
//
// Callers now wait on the registration itself (requireConnRegistered), the
// precondition they actually depend on, which is one causal step shorter:
// serveWS registers before wsAttach spawns its read pump and ping loop and
// before it marshals and writes the start frame.
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
	return conn
}

// wsHangGuardCeiling bounds every hang-guard in this file. 60s is NOT a
// latency budget: it is ~550x the 0.11s the slower of these two tests takes
// end to end (OBSERVED idle), and 12x the 5s constant that demonstrably fired
// in CI against a strictly LONGER causal path — the start frame needs two
// goroutine spawns, a JSON marshal, a TCP write and a client-side read that
// registration does not. It is also far enough under both CI ceilings (unit
// job timeout-minutes: 10, race job 20) that a hang reports as an assertion
// with minutes to spare instead of as a killed runner.
const wsHangGuardCeiling = 60 * time.Second

// hangGuardDeadline returns an absolute deadline for a wait that must NEVER
// fire in a correct run:
//
//	min(t.Deadline() - reporting margin, now + wsHangGuardCeiling)
//
// Both halves are load-bearing; neither alone is enough.
//
// WHY THIS IS NOT "JUST A BIGGER NUMBER". What the bead forbids is a bound the
// test's PASS depends on in normal operation. That is exactly what the 5s this
// file used to carry was: the start frame's ARRIVAL TIME was the assertion, so
// a busy runner turned correct code red and reported it as "not revocable"
// (agent-os-fzqb; OBSERVED in CI on PR #256 @ 8aed205 — plain unit job red,
// rerun of the IDENTICAL SHA green, and the SLOWER race job green both times,
// which is load pointing at itself). The waits below instead block on a
// CONDITION — registration in the ConnectionManager — that is satisfied in
// milliseconds under any realistic load, so no passing run ever consults this
// deadline at all.
//
// The distinction is semantic, not syntactic, and switching the observable
// alone does NOT fix the class: firstConnection
// (monitoring_metrics_close_test.go:130) polls the SAME registration
// observable on a 5s constant, and the orchestrator OBSERVED it firing under
// concurrent load on CLEAN main as `dashboard_metrics_close_test.go:65: no
// connection registered in cm within 5s`. The bound is the defect, not the
// signal.
//
// WHY THE t.Deadline() HALF. A bare constant is a number picked against an
// imagined machine. Deriving from the binary's own -timeout makes the guard
// adapt to what the invoker actually allowed, so a short -timeout run reports
// promptly instead of outliving the binary it belongs to. Its bool is FALSE
// under -timeout 0 (no deadline at all), a routine way to run this suite
// locally, which is why the ceiling has to stand on its own.
//
// WHY THE CEILING HALF — the part t.Deadline() alone gets WRONG.
// .github/workflows/backend.yml runs `go test ./... -count=1` with NO -timeout
// flag (line 116; the race job at line 156 likewise), so Go's default 10m
// package timeout applies — and the unit job's own `timeout-minutes: 10`
// (line 72) is the SAME NUMBER. A guard at t.Deadline() minus a small margin
// would fire at ~9m55s, inside the window where GitHub is already killing the
// runner: on a real hang the likely outcome is a cancelled job with NO test
// output, which is strictly worse for diagnosis than the 5s failure this
// change removes. The ceiling keeps a hang inside the job, as a named
// assertion. (OBSERVED by the orchestrator reading backend.yml, 2026-09-04.)
//
// COST, stated so it is chosen and not discovered by whoever is on call: a
// genuinely broken signal costs up to wsHangGuardCeiling before it reports.
// MEASURED, with SetConnectionManager disabled so registration never happens:
// 60.00s per test at the default -timeout, and 25.00s under `-timeout 30s`
// (the t.Deadline() half binding instead). Seconds, not minutes, either way.
func hangGuardDeadline(t *testing.T) time.Time {
	t.Helper()

	guard := time.Now().Add(wsHangGuardCeiling)
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

// requireConnRegistered blocks until cm holds a connection satisfying match —
// that is, until the precondition the caller is about to depend on holds.
//
// Reading cm.connections directly is the established pattern in this package
// (firstConnection, monitoring_metrics_close_test.go:117, documents why: this
// file is `package handlers`, same as ws.go).
//
// match is a parameter rather than a hardcoded "any connection" because each
// caller depends on a DIFFERENT registration. In the revocation test an "any"
// match would be satisfied by the `control` connection that test adds itself,
// before the handler has run at all — a wait its own setup already satisfied
// is not a wait.
func requireConnRegistered(t *testing.T, cm *ConnectionManager, match func(*Connection) bool, what string) {
	t.Helper()

	guard := hangGuardDeadline(t)
	for {
		cm.mu.RLock()
		for _, c := range cm.connections {
			if match(c) {
				cm.mu.RUnlock()
				return
			}
		}
		cm.mu.RUnlock()

		if !time.Now().Before(guard) {
			t.Fatalf("%s: no matching connection was ever registered in the ConnectionManager "+
				"(wsAttach never reached serveWS's registration step)", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// requireBackupStartFrame reads and asserts the {"type":"start"} frame wsAttach
// sends. This is the package's ONLY assertion of that protocol step
// (`command grep -rn '"start"' *_test.go` finds no other), which is why it
// survived the removal of the read from dialBackupWS — but it now lives here,
// called at a point of the caller's choosing, so it is never sitting on the
// path of something being raced against.
func requireBackupStartFrame(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var startFrame map[string]interface{}
	require.NoError(t, conn.ReadJSON(&startFrame), "reading the start frame")
	require.Equal(t, "start", startFrame["type"])
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

	// The precondition for everything below: CloseForSession(revokedJTI) can
	// only close what is registered, so wait for the backup connection to be
	// registered CARRYING THAT SessionID. Matching on the session id (not on
	// "any connection", which `control` above already satisfies) is what makes
	// this a real wait, and registration — not the start frame — is what the
	// revocation depends on.
	requireConnRegistered(t, cm, func(c *Connection) bool {
		return c.SessionID == revokedJTI
	}, "backup WS attach at the per-user cap")

	closeCode := 0
	conn.SetCloseHandler(func(code int, text string) error {
		closeCode = code
		return nil
	})
	// Hang-guard, not a budget. CloseForSession writes every close frame and
	// closes every socket before it returns (ws.go closeMatching), so in a
	// correct run the read loop below completes in microseconds. A constant
	// here would be the same defect the 5s in dialBackupWS was: if it fired,
	// closeCode would stay 0 and the failure would be reported by the
	// require.Equal below as "not revocable" rather than "the runner was slow".
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))

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

	// Wait for the backup stream's own registration before asking a
	// hard-refusing handler what the cap looks like. Without this the
	// operations dial can win the race and measure a cap the backup stream has
	// not touched yet, which passes whether or not wsAttach meters.
	requireConnRegistered(t, cm, func(c *Connection) bool {
		return c.UserID == "anonymous"
	}, "backup WS attach under AUTH_DISABLED")

	// Same manager, same user ("anonymous" under authDisabled), a handler that
	// DOES hard-refuse at the cap (operations_test.go's own fixture/helpers).
	opSrv, _ := newOperationsFixtureWith(t, cm, &fakeStreamer{})
	code, text := dialOperations(t, opSrv, "stack-a", "start")

	require.NotEqual(t, CloseCodeRateLimit, code,
		"operations WS refused (%d %q) — the backup stream consumed a cap slot a hard-refusing handler needed", code, text)

	// Protocol coverage carried over from dialBackupWS, asserted here because
	// this is the one place in the file where nothing races with it: the run is
	// blocked (buildBlockingBackupSvc) so the stream stays open, and every
	// timing-sensitive step of both tests is already done.
	requireBackupStartFrame(t, conn)
}
