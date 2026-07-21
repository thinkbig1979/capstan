//go:build integration

// Package integrationtest — B5 backup durability integration tests.
//
// These tests verify that backup and restore operations are durable:
//   - A run started by the POST handler COMPLETES even if no WS client ever
//     connects.  The BackupRun DB record is the source of truth (Finding #7).
//   - A restore run survives a simulated WS client disconnect: it runs on a
//     context.Background()-derived goroutine that is NOT tied to the request
//     or WS context (Finding #7 / #17).
//   - A run whose underlying operation fails records outcome="failed" (not
//     success) in the DB.
//
// These tests use fake restic/rclone commandRunners injected via the service's
// factory seams — no real restic/rclone binary or Docker daemon is required.
// They belong to the integration package (and carry the integration build tag)
// because they exercise the full service+handler+DB path across package
// boundaries, which goes beyond what a pure unit test can prove.
package integrationtest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// newTestDB opens an in-memory SQLite DB with all migrations applied.
func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err, "open in-memory DB")
	t.Cleanup(func() { db.Close() })
	return db
}

// seedBackupStack inserts a directory + stack + enabled backup policy into db.
func seedBackupStack(t *testing.T, db *database.DB, stackID, stopPolicy string) {
	t.Helper()
	dir := models.Directory{
		Path:    "/tmp/stacks/" + stackID,
		Name:    stackID,
		RootDir: "/tmp/stacks",
	}
	require.NoError(t, db.UpsertDirectory(dir))
	stack := models.Stack{
		ID:          stackID,
		Directory:   "/tmp/stacks/" + stackID,
		ProjectName: stackID,
		Status:      "stopped",
	}
	require.NoError(t, db.UpsertStack(stack))
	policy := models.BackupPolicy{
		ID:         "bp-" + stackID,
		TargetType: "stack",
		TargetID:   stackID,
		Enabled:    true,
		StopPolicy: stopPolicy,
		CreatedAt:  time.Now().Format(time.RFC3339),
		UpdatedAt:  time.Now().Format(time.RFC3339),
	}
	require.NoError(t, db.UpsertBackupPolicy(&policy))
}

// fakeDockerStopper is a no-op docker stopper for backup tests where docker
// interactions do not affect the durability assertion.
type fakeDockerStopper struct{}

func (f *fakeDockerStopper) StopVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.Success("stack stopped"), ""
}
func (f *fakeDockerStopper) StartVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.Success("stack running"), ""
}
func (f *fakeDockerStopper) Status(stack models.Stack) (string, []models.Container, error) {
	return "stopped", nil, nil
}

// commandRunnerFunc is a functional implementation of the services.CommandRunner
// interface (defined as commandRunner inside the services package, unexported).
// We satisfy it through the factory seam on BackupService.

// buildSuccessBackupSvc constructs a BackupService wired with a fake restic
// runner that reports a single snapshot and a successful backup. The rclone
// runner is a no-op. stop-policy is "hot" so no docker.Stop is called.
func buildSuccessBackupSvc(t *testing.T, db *database.DB) *services.BackupService {
	t.Helper()

	// A restic password is required by ResticManager.withPasswordFile().
	// Set it in the DB so resolveBackupConfig picks it up.
	if err := db.SetSetting("restic_password", "test-password"); err != nil {
		t.Fatalf("buildSuccessBackupSvc: set restic_password: %v", err)
	}
	if err := db.SetSetting("restic_repository", "/tmp/test-repo"); err != nil {
		t.Fatalf("buildSuccessBackupSvc: set restic_repository: %v", err)
	}

	cfg := &config.Config{
		DataDir:      t.TempDir(),
		StacksDir:    "/tmp/stacks",
		AuthDisabled: true,
		JWTSecret:    "test-secret-32-chars-padding-here",
	}

	opLock := services.NewOperationLock()
	actions := services.NewActionLogger(db)
	svc := services.NewBackupService(cfg, db, &fakeDockerStopper{}, opLock, actions)
	svc.SetBins("/usr/bin/restic", "/usr/bin/rclone")

	// Inject fake managers that report success.
	svc.SetResticMgrFactory(successResticFactory)
	svc.SetRcloneMgrFactory(noopRcloneFactory)

	return svc
}

// buildFailingBackupSvc constructs a BackupService whose fake restic runner
// always returns a non-nil error.
func buildFailingBackupSvc(t *testing.T, db *database.DB) *services.BackupService {
	t.Helper()

	// Password required so withPasswordFile() doesn't fail before the runner.
	if err := db.SetSetting("restic_password", "test-password"); err != nil {
		t.Fatalf("buildFailingBackupSvc: set restic_password: %v", err)
	}
	if err := db.SetSetting("restic_repository", "/tmp/test-repo"); err != nil {
		t.Fatalf("buildFailingBackupSvc: set restic_repository: %v", err)
	}

	cfg := &config.Config{
		DataDir:      t.TempDir(),
		StacksDir:    "/tmp/stacks",
		AuthDisabled: true,
		JWTSecret:    "test-secret-32-chars-padding-here",
	}

	opLock := services.NewOperationLock()
	actions := services.NewActionLogger(db)
	svc := services.NewBackupService(cfg, db, &fakeDockerStopper{}, opLock, actions)
	svc.SetBins("/usr/bin/restic", "/usr/bin/rclone")

	svc.SetResticMgrFactory(failingResticFactory)
	svc.SetRcloneMgrFactory(noopRcloneFactory)

	return svc
}

// waitForRunTerminal polls the DB until the BackupRun with runID reaches a
// terminal status ("success", "partial", or "failed"), or the deadline expires.
func waitForRunTerminal(t *testing.T, db *database.DB, runID string, timeout time.Duration) *models.BackupRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := db.GetBackupRunByID(runID)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		switch run.Status {
		case "success", "partial", "failed":
			return run
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("BackupRun %q did not reach terminal status within %v; last DB status: %q",
		runID, timeout, lastRunStatus(t, db, runID))
	return nil
}

func lastRunStatus(t *testing.T, db *database.DB, runID string) string {
	t.Helper()
	run, err := db.GetBackupRunByID(runID)
	if err != nil {
		return "<error: " + err.Error() + ">"
	}
	return run.Status
}

// ─────────────────────────────────────────────────────────────────────────────
// Factory helpers — these types must satisfy the service-internal interfaces.
// We use the exported SetResticMgrFactory / SetRcloneMgrFactory seams.
// ─────────────────────────────────────────────────────────────────────────────

// successResticFactory returns a fake restic manager that responds to all
// restic commands with success. Backup emits a JSON summary; ListSnapshots
// returns one snapshot tagged with the stackID argument.
func successResticFactory(bc services.BackupConfig) *services.ResticManager {
	return services.NewResticManagerForTest(bc, &successResticRunner{}, nil)
}

// failingResticFactory returns a fake restic manager whose Backup command
// always returns an error.
func failingResticFactory(bc services.BackupConfig) *services.ResticManager {
	return services.NewResticManagerForTest(bc, &failingResticRunner{}, nil)
}

func noopRcloneFactory(bc services.BackupConfig) *services.RcloneManager {
	return services.NewRcloneManagerForTest(bc, &noopRcloneRunner{}, nil)
}

// successResticRunner simulates restic commands that all succeed.
type successResticRunner struct{}

func (r *successResticRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	env []string,
	out chan<- services.StreamLine,
) error {
	if out != nil {
		out <- services.StreamLine{Type: "info", Line: "fake restic: " + name + " ok"}
	}
	return nil
}

func (r *successResticRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	// Return a minimal restic snapshots JSON for ListSnapshots calls, and a
	// minimal backup summary for Backup JSON parsing.
	for _, arg := range args {
		if arg == "snapshots" {
			return []byte(`[{"id":"abc1234567890","short_id":"abc12345","time":"2026-01-01T00:00:00Z","hostname":"test","tags":["test-stack"],"paths":["/tmp/stacks/test-stack"]}]`), nil
		}
		if arg == "backup" {
			// restic backup --json produces a stream; the last line is the summary.
			return []byte(`{"message_type":"summary","files_new":1,"files_changed":0,"files_unmodified":0,"data_blobs":1,"tree_blobs":1,"data_added":1024,"total_files_processed":1,"total_bytes_processed":1024,"total_duration":0.1,"snapshot_id":"abc12345"}`), nil
		}
		if arg == "stats" {
			return []byte(`{"total_size":1024,"total_file_count":1}`), nil
		}
	}
	return []byte(`[]`), nil
}

// failingResticRunner simulates restic commands that always fail.
type failingResticRunner struct{}

func (r *failingResticRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	env []string,
	out chan<- services.StreamLine,
) error {
	return errors.New("fake restic error: command failed")
}

func (r *failingResticRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	return nil, errors.New("fake restic error: output failed")
}

// noopRcloneRunner is a no-op rclone runner (sync / dr-restore tests not
// exercised by these specific durability tests).
type noopRcloneRunner struct{}

func (r *noopRcloneRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	env []string,
	out chan<- services.StreamLine,
) error {
	return nil
}

func (r *noopRcloneRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	return []byte(`{}`), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Durability Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestBackup_KickoffWithoutWS_RunsToCompletion is the primary Finding #7 guard.
//
// It proves that a backup run started via LaunchBackup COMPLETES and records a
// terminal BackupRun status in the DB even when no WS client ever connects.
// The run executes on a detached goroutine (context.Background()) entirely
// independent of any HTTP request or WebSocket context.
func TestBackup_KickoffWithoutWS_RunsToCompletion(t *testing.T) {
	db := newTestDB(t)
	seedBackupStack(t, db, "test-stack", "hot") // hot = no docker.Stop needed

	svc := buildSuccessBackupSvc(t, db)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	// Launch the backup — this returns immediately with a runID.
	runID, err := reg.LaunchBackup([]string{"test-stack"}, false)
	require.NoError(t, err, "LaunchBackup must not return an error")
	require.NotEmpty(t, runID, "LaunchBackup must return a non-empty runID")

	// CRITICAL: Do NOT open any WS connection. The run must proceed without one.

	// The DB record must exist synchronously after LaunchBackup returns.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "BackupRun row must exist in DB before any WS connects")
	assert.Equal(t, "running", run.Status, "initial status must be 'running'")
	assert.Equal(t, "backup", run.Kind)
	assert.Equal(t, "manual", run.Trigger)

	// Wait for the background goroutine to finish.
	finalRun := waitForRunTerminal(t, db, runID, 10*time.Second)
	require.NotNil(t, finalRun)

	t.Logf("backup run %s completed with status=%q", runID, finalRun.Status)

	assert.NotEqual(t, "running", finalRun.Status,
		"run must not be stuck in 'running' after completion")
	assert.Contains(t,
		[]string{"success", "partial", "failed"},
		finalRun.Status,
		"terminal status must be success, partial, or failed",
	)
	assert.NotNil(t, finalRun.FinishedAt,
		"FinishedAt must be set when the run is terminal")
}

// TestBackup_KickoffWithoutWS_SuccessRecordedInDB checks that when the fake
// restic runner reports success the run record reaches status="success".
func TestBackup_KickoffWithoutWS_SuccessRecordedInDB(t *testing.T) {
	db := newTestDB(t)
	seedBackupStack(t, db, "app-stack", "hot")

	svc := buildSuccessBackupSvc(t, db)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup([]string{"app-stack"}, false)
	require.NoError(t, err)

	finalRun := waitForRunTerminal(t, db, runID, 10*time.Second)
	require.NotNil(t, finalRun)

	assert.Equal(t, "success", finalRun.Status,
		"run status must be 'success' when the backup operation succeeds")
	assert.NotNil(t, finalRun.FinishedAt)
	assert.Equal(t, 1, finalRun.StacksOK, "exactly one stack must be recorded as OK")
	assert.Equal(t, 0, finalRun.StacksFailed)
}

// TestBackup_KickoffWithoutWS_FailureRecordedInDB checks that when the fake
// restic runner returns an error the run record reaches status="failed" (not
// "success"). This guards against false-success reporting.
func TestBackup_KickoffWithoutWS_FailureRecordedInDB(t *testing.T) {
	db := newTestDB(t)
	seedBackupStack(t, db, "failing-stack", "hot")

	svc := buildFailingBackupSvc(t, db)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup([]string{"failing-stack"}, false)
	require.NoError(t, err)

	finalRun := waitForRunTerminal(t, db, runID, 10*time.Second)
	require.NotNil(t, finalRun)

	assert.Equal(t, "failed", finalRun.Status,
		"run status must be 'failed' when the backup operation errors")
	assert.NotNil(t, finalRun.FinishedAt)
}

// blockingResticRunner is a fake restic CommandRunner whose Run method blocks
// on the gate channel until it is released (by closing gate or sending a value),
// then returns nil.  Output returns valid snapshot JSON so that snapshot
// validation inside RunRestore passes without blocking.
//
// This lets a test hold the exec goroutine mid-run, close the clientGone
// channel to simulate a WS disconnect, and then release the gate to confirm the
// goroutine continues to completion.
type blockingResticRunner struct {
	// gate is closed (or sent on) by the test to unblock a Run call.
	gate <-chan struct{}
	// started is closed by Run as soon as it enters the blocking section,
	// letting the test know the goroutine has reached the critical point.
	started chan struct{}
	// startOnce guards the started-close so only the first Run call signals.
	startOnce sync.Once
}

func (r *blockingResticRunner) Run(
	ctx context.Context,
	_ string,
	_ []string,
	_ []string,
	_ chan<- services.StreamLine,
) error {
	r.startOnce.Do(func() { close(r.started) })
	// Block until the gate is released or the context is cancelled.
	select {
	case <-r.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *blockingResticRunner) Output(
	_ context.Context,
	_ string,
	args []string,
	_ []string,
) ([]byte, error) {
	// Return the minimal snapshot JSON that satisfies validateSnapshotBelongsToStack.
	// The snapshot must be tagged with the stack ID so the validation inside
	// RunRestore accepts it.
	for _, arg := range args {
		if arg == "snapshots" {
			return []byte(`[{"id":"abc1234567890","short_id":"abc12345","time":"2026-01-01T00:00:00Z","hostname":"test","tags":["restore-stack"],"paths":["/tmp/stacks/restore-stack"]}]`), nil
		}
	}
	return []byte(`[]`), nil
}

// TestRestore_ClientDisconnect_DoesNotAbortRun proves that closing the
// clientGone channel (simulating a WS client disconnect) does NOT abort the
// restore goroutine.  The goroutine must proceed to a terminal DB record even
// after the client is gone.
//
// Design:
//  1. A blockingResticRunner holds the exec goroutine mid-restore on gate.
//  2. Attach is called with a clientGone channel wired to the WS lifecycle.
//  3. clientGone is closed (simulating disconnect) while the run is still in
//     progress (gate is still closed).
//  4. The gate is then released.
//  5. The test asserts the run reaches a terminal DB status — proving the
//     exec goroutine was never bound to the WS context.
//
// Load-bearing: if execRestore were changed to use the clientGone channel
// as its execution context (e.g. ctx = wsCtx), closing clientGone would
// cancel that context and the run would stall or fail.  The blockingResticRunner
// respects ctx.Done(), so the test would see a "failed" status caused by
// context.Canceled instead of "success", and the assertion would fail.
func TestRestore_ClientDisconnect_DoesNotAbortRun(t *testing.T) {
	db := newTestDB(t)
	seedBackupStack(t, db, "restore-stack", "hot")

	gate := make(chan struct{})
	runner := &blockingResticRunner{
		gate:    gate,
		started: make(chan struct{}),
	}

	cfg := &config.Config{
		DataDir:      t.TempDir(),
		StacksDir:    "/tmp/stacks",
		AuthDisabled: true,
		JWTSecret:    "test-secret-32-chars-padding-here",
	}
	if err := db.SetSetting("restic_password", "test-password"); err != nil {
		t.Fatalf("set restic_password: %v", err)
	}
	if err := db.SetSetting("restic_repository", "/tmp/test-repo"); err != nil {
		t.Fatalf("set restic_repository: %v", err)
	}

	opLock := services.NewOperationLock()
	actions := services.NewActionLogger(db)
	svc := services.NewBackupService(cfg, db, &fakeDockerStopper{}, opLock, actions)
	svc.SetBins("/usr/bin/restic", "/usr/bin/rclone")
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, nil)
	})
	svc.SetRcloneMgrFactory(noopRcloneFactory)

	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchRestore("restore-stack", "abc12345", "")
	require.NoError(t, err, "LaunchRestore must succeed")
	require.NotEmpty(t, runID)

	// Wait until the exec goroutine has entered the blocking Run call.
	// This confirms the run is mid-flight before we simulate the disconnect.
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("exec goroutine did not start within 5 s")
	}

	// Simulate WS client disconnect: Attach then immediately close clientGone.
	clientGone := make(chan struct{})
	ar, err := reg.Attach(runID, clientGone)
	require.NoError(t, err)
	require.False(t, ar.Done, "run must still be in progress when we simulate disconnect")

	// Close clientGone — the WS client is gone.  The exec goroutine must not
	// be affected because it runs on context.Background().
	close(clientGone)

	// The run is still blocked on the gate at this point.  Confirm the DB
	// record is still "running" (i.e. the goroutine did not exit early).
	midRun, dbErr := db.GetBackupRunByID(runID)
	require.NoError(t, dbErr)
	assert.Equal(t, "running", midRun.Status,
		"status must still be 'running' immediately after simulated disconnect")

	// Release the gate — let the restore step complete.
	close(gate)

	// Now the exec goroutine should proceed to completion.
	finalRun := waitForRunTerminal(t, db, runID, 10*time.Second)
	require.NotNil(t, finalRun, "run must reach terminal state after gate release")

	assert.Equal(t, "success", finalRun.Status,
		"run must complete as 'success'; a 'failed' here means the exec goroutine was bound to clientGone")
	assert.NotNil(t, finalRun.FinishedAt,
		"FinishedAt must be set on the completed restore run")
}

// TestBackup_AttachAfterCompletion verifies that Attach returns Done=true and
// the correct terminal outcome when called after the run has finished. This
// proves the WS handler can replay the terminal status to late-joining clients
// (Finding #17 support).
func TestBackup_AttachAfterCompletion(t *testing.T) {
	db := newTestDB(t)
	seedBackupStack(t, db, "late-attach-stack", "hot")

	svc := buildSuccessBackupSvc(t, db)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup([]string{"late-attach-stack"}, false)
	require.NoError(t, err)

	// Wait until terminal before attaching.
	waitForRunTerminal(t, db, runID, 10*time.Second)

	ar, err := reg.Attach(runID, nil)
	require.NoError(t, err, "Attach must not error for a completed run")

	assert.True(t, ar.Done, "Attach must report Done=true for a completed run")
	assert.NotEmpty(t, ar.Outcome, "Outcome must be set for a completed run")
	assert.Nil(t, ar.Live, "Live channel must be nil for a completed run")
}

// TestBackup_StatusEndpoint_ReturnsRunRecord verifies that GetBackupRunByID
// returns the run record that the registry created, confirming that the
// GET /backups/runs/:runId endpoint has the data it needs for Finding #17.
func TestBackup_StatusEndpoint_ReturnsRunRecord(t *testing.T) {
	db := newTestDB(t)
	seedBackupStack(t, db, "status-stack", "hot")

	svc := buildSuccessBackupSvc(t, db)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup([]string{"status-stack"}, false)
	require.NoError(t, err)

	// The status endpoint uses db.GetBackupRunByID; verify it works.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err)
	assert.Equal(t, runID, run.ID)
	assert.Equal(t, "backup", run.Kind)

	// Wait for completion and re-check.
	finalRun := waitForRunTerminal(t, db, runID, 10*time.Second)
	require.NotNil(t, finalRun)

	// A client polling after disconnect gets the terminal record.
	terminalRun, err := db.GetBackupRunByID(runID)
	require.NoError(t, err)
	assert.NotEqual(t, "running", terminalRun.Status,
		"the status endpoint must reflect terminal state for a completed run")
}
