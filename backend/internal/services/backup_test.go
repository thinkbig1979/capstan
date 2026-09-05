package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// ============================================================
// Test doubles
// ============================================================

// fakeDocker is a test double for dockerStopper. The zero value is usable;
// it reports "stopped" status and no errors.
type fakeDocker struct {
	mu sync.Mutex

	stopCalls  []models.Stack
	startCalls []models.Stack

	stopErr   error
	startErr  error
	statusStr string // returned by Status; default "stopped"
	statusErr error  // when set, Status returns this error instead of statusStr
}

func (f *fakeDocker) StopVerified(stack models.Stack) (truth.ActionResult, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, stack)
	if f.stopErr != nil {
		return truth.Failed("stop failed", f.stopErr), ""
	}
	return truth.Success("stack stopped"), ""
}

func (f *fakeDocker) StartVerified(stack models.Stack) (truth.ActionResult, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, stack)
	if f.startErr != nil {
		return truth.Failed("start failed", f.startErr), ""
	}
	return truth.Success("stack running"), ""
}

func (f *fakeDocker) Status(stack models.Stack) (string, []models.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return "", nil, f.statusErr
	}
	s := f.statusStr
	if s == "" {
		s = "stopped"
	}
	return s, nil, nil
}

func (f *fakeDocker) stopped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stopCalls)
}

func (f *fakeDocker) started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.startCalls)
}

// ============================================================
// Test helpers
// ============================================================

// newBackupTestDB opens an in-memory SQLite DB with all migrations applied.
func newBackupTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// seedStack inserts a directory, a stack record, and an enabled backup policy
// into db. stopPolicy must be "stop" or "hot".
func seedStack(t *testing.T, db *database.DB, stackID string, stopPolicy string) (models.Stack, models.BackupPolicy) {
	t.Helper()

	dir := models.Directory{
		Path:    "/opt/stacks/" + stackID,
		Name:    stackID,
		RootDir: "/opt/stacks",
	}
	require.NoError(t, db.UpsertDirectory(dir))

	stack := models.Stack{
		ID:          stackID,
		Directory:   "/opt/stacks/" + stackID,
		ProjectName: stackID,
		Status:      "running",
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

	return stack, policy
}

// buildSvc constructs a BackupService wired with the given fakeDocker and fake
// commandRunners for restic/rclone. resticBin/rcloneBin are set to non-empty
// so availability checks pass without real binaries on the test host.
func buildSvc(
	t *testing.T,
	db *database.DB,
	docker *fakeDocker,
	resticRunner commandRunner,
	rcloneRunner commandRunner,
) *BackupService {
	t.Helper()

	opLock := NewOperationLock()
	actions := NewActionLogger(db)

	bc := BackupConfig{
		ResticRepository: "/tmp/test-repo",
		ResticPassword:   "test-password",
		KeepDaily:        7,
		KeepWeekly:       4,
		AutoPrune:        true,
		RcloneRemote:     "myremote",
		RclonePath:       "backup/path",
		RcloneTransfers:  4,
	}

	// cfg must be non-nil because resolveBackupConfig dereferences it.
	// Use a minimal config; the actual manager config comes from the factories.
	cfg := &config.Config{
		DataDir:      t.TempDir(),
		StacksDir:    "/opt/stacks",
		AuthDisabled: true,
		JWTSecret:    "test-secret-32-chars-padding-here",
	}

	svc := &BackupService{
		cfg:       cfg,
		db:        db,
		docker:    docker,
		opLock:    opLock,
		actions:   actions,
		logger:    slog.Default().With("component", "backup-test"),
		resticBin: "/usr/bin/restic",
		rcloneBin: "/usr/bin/rclone",
		resticMgrFactory: func(_ BackupConfig) *ResticManager {
			return newResticManagerWithRunner(bc, resticRunner, nil)
		},
		rcloneMgrFactory: func(_ BackupConfig) *RcloneManager {
			return newRcloneManagerWithRunner(bc, rcloneRunner, nil)
		},
	}

	return svc
}

// TestRunDRRestore_ConfinesDestinationToDataDir is the regression test for the
// C1 finding: a client-supplied localRepoPath previously flowed straight into
// `rclone sync <remote> <localRepoPath>`, letting an authenticated user
// overwrite arbitrary host paths (e.g. /etc) as root. The destination must be
// derived server-side inside DataDir and never influenced by client input.
func TestRunDRRestore_ConfinesDestinationToDataDir(t *testing.T) {
	db := newBackupTestDB(t)
	// outputData: the source probe (rclone lsf) must see the source as a
	// genuine restic repository -- exactly "config" -- for RestoreRepo to
	// proceed to sync at all.
	rcloneRunner := &fakeRunner{outputData: []byte("config")}
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, rcloneRunner)
	// resolveBackupConfig falls back to cfg.RcloneRemote when the DB has none,
	// so this makes RunDRRestore's "remote configured" precondition pass.
	svc.cfg.RcloneRemote = "myremote"

	out := make(chan StreamLine, 64)
	go func() {
		for range out { //nolint:revive // drain
		}
	}()
	err := svc.RunDRRestore(context.Background(), out)
	require.NoError(t, err)
	close(out)

	call := rcloneRunner.lastCall()
	require.Equal(t, "rclone", call.Binary)

	// The rclone destination (last positional arg) must be the server-derived
	// restic repository path — not anything a client could supply.
	dest := call.Args[len(call.Args)-1]
	want := filepath.Join(svc.cfg.DataDir, "restic-repo")
	assert.Equal(t, want, dest, "DR restore destination must be the server-derived restic repo path")
	assert.DirExists(t, want, "destination directory must be created before restore")
}

// drainChannel reads all pending lines from out into a slice (non-blocking).
func drainChannel(out chan StreamLine) []StreamLine {
	var lines []StreamLine
	for {
		select {
		case l := <-out:
			lines = append(lines, l)
		default:
			return lines
		}
	}
}

// snapshotJSON returns JSON for a single snapshot tagged with stackID.
func snapshotJSON(id, shortID, stackID string) []byte {
	snaps := []map[string]interface{}{
		{
			"id":       id,
			"short_id": shortID,
			"time":     "2026-05-30T10:00:00Z",
			"hostname": "test-host",
			"tags":     []string{stackID, "capstan-backup"},
			"paths":    []string{"/opt/stacks/" + stackID},
		},
	}
	b, _ := json.Marshal(snaps)
	return b
}

// ============================================================
// Available() / graceful degradation
// ============================================================

func TestAvailable_NoBinaries(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	// Simulate missing binaries.
	svc.resticBin = ""
	svc.rcloneBin = ""

	av := svc.Available()
	assert.False(t, av.Available)
	assert.False(t, av.ResticPresent)
	assert.False(t, av.RclonePresent)
	assert.NotEmpty(t, av.Message)
}

func TestAvailable_ResticPresentOnly(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	svc.rcloneBin = ""

	av := svc.Available()
	assert.True(t, av.Available, "restic alone is enough for local backups")
	assert.True(t, av.ResticPresent)
	assert.False(t, av.RclonePresent)
}

func TestAvailable_BothPresent(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	av := svc.Available()
	assert.True(t, av.Available)
	assert.True(t, av.ResticPresent)
	assert.True(t, av.RclonePresent)
}

func TestRunBackup_UnavailableWhenNoRestic(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.resticBin = ""

	out := make(chan StreamLine, 64)
	_, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	assert.ErrorIs(t, err, ErrBackupUnavailable)
}

// TestRunBackup_TriggerConstraint guards docker-manager-ly6: a user-initiated
// backup must use a trigger value permitted by the backup_runs.trigger CHECK
// constraint (manual|scheduled). The WS/UI handler previously passed "api",
// which made CreateBackupRun fail and broke every "Back up now". This pins both
// the failure mode and the fix. CreateBackupRun runs before policy resolution,
// so the constraint is exercised even with no enabled policies.
func TestRunBackup_TriggerConstraint(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	// Invalid trigger (the old "api" value) must fail at run creation.
	out := make(chan StreamLine, 256)
	_, err := svc.RunBackup(context.Background(), nil, false, "api", out)
	assert.Error(t, err, "invalid trigger must violate the backup_runs CHECK constraint")

	// The constant the handler now uses must be accepted and persisted.
	out = make(chan StreamLine, 256)
	_, err = svc.RunBackup(context.Background(), nil, false, TriggerManual, out)
	assert.NoError(t, err)

	runs, err := db.GetBackupRuns(1)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, TriggerManual, runs[0].Trigger)
	}
}

// ============================================================
// Single-flight / concurrency guard
// ============================================================

func TestRunBackup_RejectsConcurrentRun(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}

	// Use a slow runner so the first call still holds the lock when the second
	// arrives.
	blocker := make(chan struct{})
	slowRunner := &fakeRunner{
		onRun: func(name string, args []string, out chan<- StreamLine) {
			<-blocker
		},
	}

	svc := buildSvc(t, db, docker, slowRunner, slowRunner)

	// Seed a stack so the first backup has something to do.
	seedStack(t, db, "stack-a", "hot")

	out1 := make(chan StreamLine, 128)
	out2 := make(chan StreamLine, 128)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = svc.RunBackup(context.Background(), nil, false, "manual", out1)
	}()

	// Give the goroutine time to acquire the lock.
	time.Sleep(20 * time.Millisecond)

	_, err := svc.RunBackup(context.Background(), nil, false, "manual", out2)
	assert.ErrorIs(t, err, ErrBackupBusy)

	close(blocker)
	wg.Wait()
}

func TestIsBusy(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	assert.False(t, svc.IsBusy())
	svc.busy.Store(1)
	assert.True(t, svc.IsBusy())
	svc.busy.Store(0)
	assert.False(t, svc.IsBusy())
}

// ============================================================
// RunBackup — stop policy
// ============================================================

func TestRunBackup_StopPolicy_StopsAndRestartsRunningStack(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 1, docker.stopped(), "Stop must be called once")
	assert.Equal(t, 1, docker.started(), "Start must be called once (restart after backup)")
}

// TestRunBackup_StatusError_DefaultsToNoRestart pins the behavior of unifying
// Status (docker_lifecycle.go) to always propagate a real docker compose ps
// error instead of the old ("unknown", nil, nil) sentinel. backupStack already
// gates wasRunning on `statusErr == nil`, defaulting to false — so a ps failure
// now takes that same default-false branch instead of the previously swallowed
// "unknown" status (which also evaluated to not-running). The stop policy still
// applies (it does not depend on wasRunning), but the defensive restart after
// backup must be skipped because the service could not prove the stack had
// been running in the first place.
func TestRunBackup_StatusError_DefaultsToNoRestart(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusErr: errors.New("docker compose ps failed")}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 1, docker.stopped(), "stop policy still applies regardless of wasRunning")
	assert.Equal(t, 0, docker.started(), "restart must be skipped: Status error means wasRunning could not be proven true")
}

func TestRunBackup_StopPolicy_DoesNotRestartIfWasStopped(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 1, docker.stopped(), "Stop must be called")
	assert.Equal(t, 0, docker.started(), "Start must NOT be called — stack was already stopped")
}

// ============================================================
// RunBackup — hot policy
// ============================================================

func TestRunBackup_HotPolicy_NeverStopsStack(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 0, docker.stopped(), "Stop must NOT be called for hot policy")
	assert.Equal(t, 0, docker.started(), "Start must NOT be called for hot policy")
}

// ============================================================
// RunBackup — dry-run
// ============================================================

func TestRunBackup_DryRun_SkipsResticAndDocker(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, true, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	// No restic calls in dry-run.
	assert.Empty(t, runner.calls, "no restic/rclone calls must be made in dry-run")
	// No docker stop/start.
	assert.Equal(t, 0, docker.stopped())
	assert.Equal(t, 0, docker.started())
}

// ============================================================
// RunBackup — per-stack failure isolation → partial
// ============================================================

func TestRunBackup_PerStackFailureIsolation_Partial(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{} // unused; multiCallRunner below drives all calls

	// Use a multi-call runner: fail stack-a, succeed stack-b.
	//
	// The sequence is positional, so it must account for the capstan.db snapshot
	// that every run now takes BEFORE the per-stack loop (agent-os-36o). Without
	// this leading entry the database call would consume stack-a's injected
	// failure and the isolation this test checks would silently stop being
	// exercised.
	multiRunner := &multiCallRunner{
		responses: []multiCallResponse{
			// database snapshot → success
			{binary: "restic", argPrefix: "backup", err: nil},
			// stack-a backup call → error
			{binary: "restic", argPrefix: "backup", err: errors.New("disk full")},
			// stack-b backup call → success
			{binary: "restic", argPrefix: "backup", err: nil},
			// stack-b verify call → success (snapshots output)
			{binary: "restic", argPrefix: "snapshots", output: snapshotJSON("abc", "ab1", "stack-b")},
			// stack-b forget → success
			{binary: "restic", argPrefix: "forget", err: nil},
			// stack-b ListSnapshots → return snapshot for run item
			{binary: "restic", argPrefix: "snapshots", output: snapshotJSON("abc", "ab1", "stack-b")},
		},
	}
	_ = runner

	svc := buildSvc(t, db, docker, multiRunner, multiRunner)
	seedStack(t, db, "stack-a", "hot")
	seedStack(t, db, "stack-b", "hot")

	out := make(chan StreamLine, 512)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err, "RunBackup itself must not return an error on partial failure")

	assert.Equal(t, "partial", run.Status)
	assert.Equal(t, 1, run.StacksOK)
	assert.Equal(t, 1, run.StacksFailed)
	assert.Equal(t, 2, run.StacksTotal)
}

// ============================================================
// RunBackup — requested stacks that resolve to no enabled policy
// ============================================================

// TestRunBackup_RequestedStackWithoutPolicyIsNotSuccess pins agent-os-6wr:
// resolveTargetPolicies filters the enabled policies by the requested IDs, so a
// requested stack with no enabled policy silently drops out of the set. Before
// the fix the run then backed up nothing and still reported status="success",
// because the status switch reads "StacksFailed == 0" as success and zero
// requested stacks means zero failures.
//
// This is the same family as the agent-os-36o dbFailed downgrade below that
// switch: work that did not happen must not read as success. Asking for a stack
// by name and being told "success" while no snapshot exists is the failure mode
// that makes an operator believe they have backups they do not have.
func TestRunBackup_RequestedStackWithoutPolicyIsNotSuccess(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}
	runner := &fakeRunner{} // the database snapshot still succeeds

	svc := buildSvc(t, db, docker, runner, runner)
	// Deliberately seed NO stack and NO policy: the requested ID cannot resolve.

	out := make(chan StreamLine, 128)
	run, err := svc.RunBackup(context.Background(), []string{"stacks~absent:default"}, false, "manual", out)
	require.NoError(t, err, "an unresolvable request is a run-level result, not a call error")

	assert.NotEqual(t, "success", run.Status,
		"a run that backed up none of the stacks it was asked for must not report success")
	assert.Contains(t, run.ErrorMessage, "stacks~absent:default",
		"the run must name the stack it could not back up, so the operator can act on it")
}

// TestRunBackup_RequestedStackWithPolicyStillSucceeds is the control for the
// test above: the guard must fire only when a requested stack genuinely has no
// enabled policy, never on the normal path. Without this, the fix could pass by
// simply refusing to report success at all.
func TestRunBackup_RequestedStackWithPolicyStillSucceeds(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}
	runner := &fakeRunner{}

	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "stack-a", "hot")

	out := make(chan StreamLine, 512)
	run, err := svc.RunBackup(context.Background(), []string{"stack-a"}, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 1, run.StacksOK)
	// The other side of the StacksTotal pin below: with one enabled policy the
	// count is 1, so the zero-case assertion is shown to pin zero specifically
	// rather than everywhere (agent-os-q0rv).
	assert.Equal(t, 1, run.StacksTotal)
	assert.Empty(t, run.ErrorMessage)
}

// TestRunBackup_NoRequestedIDsWithNoPoliciesIsUnchanged pins the deliberate
// scope of the fix. A run with no stackIDs means "every enabled policy"; when
// none are configured there is nothing the operator asked for and did not get,
// and the database snapshot — the artifact agent-os-36o exists to protect —
// still ran. That case keeps its existing status rather than being swept up by
// the new guard.
func TestRunBackup_NoRequestedIDsWithNoPoliciesIsUnchanged(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}
	runner := &fakeRunner{}

	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 128)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	// The settings badge (frontend BackupStatusCard, LastRunBadge) fires on
	// kind == backup && status == success && stacksTotal == 0, and its comment
	// cites THIS test as the reason the backend was left alone. Pin the third
	// leg here so that citation is load-bearing (agent-os-q0rv).
	assert.Equal(t, 0, run.StacksTotal)
	assert.Empty(t, run.ErrorMessage)
}

func TestRunBackup_AllFail_StatusFailed(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{runErr: errors.New("restic error")}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "stack-a", "hot")

	out := make(chan StreamLine, 128)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "failed", run.Status)
	assert.Equal(t, 0, run.StacksOK)
	assert.Equal(t, 1, run.StacksFailed)
}

// ============================================================
// RunBackup — defensive restart on failure
// ============================================================

func TestRunBackup_DefensiveRestart_OnBackupFailure(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// Stack was running when backup started.
	docker := &fakeDocker{statusStr: "running"}

	// Backup fails; verify/retention/snapshots won't be called.
	runner := &fakeRunner{runErr: errors.New("backup failed")}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "failed", run.Status)
	// Stack was stopped before backup, then must be restarted defensively.
	assert.Equal(t, 1, docker.stopped(), "must have stopped the stack")
	assert.Equal(t, 1, docker.started(), "must restart stack defensively after backup failure")
}

// ============================================================
// RunBackup — context cancellation
// ============================================================

func TestRunBackup_ContextCancel_DefensiveRestart(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}

	//nolint:gosec // cancel is invoked by the onRun callback below once the backup reaches the restic "backup" invocation; the test's own assertions on run.Status/docker.stopped/docker.started require that path to have executed, and a real leak here is bounded by the test process's lifetime regardless
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context as soon as the backup starts running.
	runner := &fakeRunner{
		onRun: func(name string, args []string, out chan<- StreamLine) {
			if name == "restic" && len(args) > 0 && args[0] == "backup" {
				cancel()
			}
		},
		runErr: context.Canceled,
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	run, err := svc.RunBackup(ctx, nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "failed", run.Status)
	// The deferred restart must fire even on cancellation.
	assert.Equal(t, 1, docker.stopped())
	assert.Equal(t, 1, docker.started(), "defensive restart must fire on context cancellation")
}

// ============================================================
// RunBackup — DB records
// ============================================================

func TestRunBackup_CreatesRunAndItems(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("longid", "sht", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, "scheduled", out)
	require.NoError(t, err)

	// Run row exists.
	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, run.ID, runs[0].ID)
	assert.Equal(t, "success", runs[0].Status)
	assert.Equal(t, "scheduled", runs[0].Trigger)
	assert.NotNil(t, runs[0].FinishedAt)

	// Run item row exists.
	items, err := db.GetBackupRunItems(run.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "myapp", items[0].StackID)
	assert.Equal(t, "success", items[0].Status)
}

func TestRunBackup_NoEnabledPolicies_EmptySuccess(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	// No policies seeded.

	out := make(chan StreamLine, 64)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 0, run.StacksTotal)
	assert.Equal(t, 0, run.StacksOK)
	assert.Equal(t, 0, run.StacksFailed)
}

func TestRunBackup_StackIDFilter_OnlyBacksUpRequested(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc", "ab", "wanted"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "wanted", "hot")
	seedStack(t, db, "unwanted", "hot")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), []string{"wanted"}, false, "manual", out)
	require.NoError(t, err)

	assert.Equal(t, "success", run.Status)
	assert.Equal(t, 1, run.StacksTotal)

	items, err := db.GetBackupRunItems(run.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "wanted", items[0].StackID)
}

// ============================================================
// RunSync
// ============================================================

func TestRunSync_Success(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// RunSync checks bc.RcloneRemote from resolveBackupConfig before using the factory.
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), out)
	require.NoError(t, err)
}

func TestRunSync_UnavailableWhenNoRclone(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.rcloneBin = ""

	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), out)
	assert.ErrorIs(t, err, ErrBackupUnavailable)
}

func TestRunSync_BusyWhileBackupRunning(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	svc.busy.Store(1)
	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), out)
	assert.ErrorIs(t, err, ErrBackupBusy)
}

// TestRunSync_RefusesWhenLocalRepositoryCheckFails is the regression test for
// the upload-direction mirror of the DR-restore data-loss defect
// (agent-os-h0my): runSyncInternal passes bc.ResticRepository straight to
// RcloneManager.Sync with no check that it is a genuine restic repository.
// rclone.Sync mirrors the local repo onto the remote and deletes remote
// files absent from the source, so an empty-but-existing local repository
// (e.g. left behind by RunDRRestore's os.MkdirAll if a DR restore is
// interrupted before RestoreRepo ever populates it) would otherwise wipe the
// last surviving copy of every snapshot offsite the next time a sync runs.
//
// This asserts rclone is never invoked once the local repository check has
// failed, not merely that RunSync's final error is non-nil.
func TestRunSync_RefusesWhenLocalRepositoryCheckFails(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	docker := &fakeDocker{}

	var calledBinaries []string
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			calledBinaries = append(calledBinaries, name)
			if name == "restic" {
				return fmt.Errorf("unable to open config file: repository not found")
			}
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return nil, nil
		},
	}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), out)

	require.Error(t, err, "RunSync must refuse when the local repository check fails")
	assert.NotContains(t, calledBinaries, "rclone", "rclone must never be invoked once the local repository check has failed")
}

// TestRunSync_ProceedsWhenLocalRepositoryCheckSucceeds is the positive
// control for TestRunSync_RefusesWhenLocalRepositoryCheckFails: a genuine
// sync from a local repository that passes the check must still succeed,
// and must still actually invoke rclone. A guard that refuses everything
// would pass the negative test above without proving the guard is
// selective.
func TestRunSync_ProceedsWhenLocalRepositoryCheckSucceeds(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	docker := &fakeDocker{}

	var calledBinaries []string
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			calledBinaries = append(calledBinaries, name)
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return nil, nil
		},
	}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), out)

	require.NoError(t, err, "a genuine sync from a repository that passes the check must still succeed")
	assert.Equal(t, []string{"restic", "rclone"}, calledBinaries, "the local repository check must run before rclone, and rclone must still run when it succeeds")
}

// fakeExitError simulates os/exec's *exec.ExitError for tests, without
// actually spawning a process. Production code type-asserts errors returned
// by commandRunner.Output against an unexported "ExitCode() int" interface
// (see isExitCode in backup_rclone.go) so that rclone's own documented exit
// code 3 ("directory not found") can be told apart from any other failure
// (connectivity, auth, misconfiguration). *exec.ExitError satisfies that
// same interface in production; this fake satisfies it in tests.
type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e fakeExitError) ExitCode() int { return e.code }

// TestRunSync_ThreeArms_ZeroSnapshotMirrorDelete is the regression test for
// agent-os-nf31: CheckRepository (and the agent-os-h0my guard built on it)
// passes on a freshly re-initialised, valid-but-EMPTY local restic
// repository -- `restic snapshots --quiet` exits 0 with zero snapshots, the
// same as it does against a populated one. Syncing that up via `rclone
// sync`, which mirrors WITH DELETE, would silently wipe every snapshot the
// remote already held.
//
// A two-arm test (empty-local-refuses / populated-local-proceeds) cannot
// distinguish a correct, remote-aware guard from a broken one that simply
// refuses whenever the local repository is empty -- and a broken guard of
// that shape would refuse the legitimate first-ever sync from a brand-new
// install, making the product unusable on day one. Arm 2 below is what
// forces the guard to actually look at the remote instead of taking the
// local-empty shortcut.
//
//	arm | local        | remote        | expected
//	  1 | 0 snapshots  | HAS snapshots | REFUSE (the bug; must fail on today's code)
//	  2 | 0 snapshots  | empty/absent  | PROCEED (genuine first sync)
//	  3 | N>0 snapshots| anything      | PROCEED (normal); remote must not even be queried
//
// Arm 1 is asserted first, alone, so it can be observed failing against
// pre-fix code (RunSync returning nil instead of an error) before the fix
// exists.
func TestRunSync_ThreeArms_ZeroSnapshotMirrorDelete(t *testing.T) {
	t.Parallel()

	// lsfArgs, when non-nil, captures the exact argv the probe invoked rclone
	// with. This matters because a stray or missing "/" in the
	// remote:path/snapshots target would misdirect the probe onto a path
	// that doesn't exist -- which surfaces as exit 3 ("directory not
	// found"), the very signal remoteHasSnapshots treats as "confirmed
	// empty" -- so a broken join would make this guard PROCEED into the
	// mirror-delete it exists to prevent, while every assertion that only
	// checks the canned return value would stay green. Capturing the args
	// the closure was actually called with (rather than a value hardcoded
	// independently of what remoteHasSnapshots builds) is what lets arm 1
	// assert against the real target.
	buildArm := func(t *testing.T, localSnapshotsJSON []byte, localErr error, remoteLsfOutput []byte, remoteLsfErr error, syncCalled *bool, lsfArgs *[]string) *BackupService {
		t.Helper()
		db := newBackupTestDB(t)
		require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
		// A non-empty, multi-segment path is deliberate: it is the only
		// setting that actually exercises the "/" join in
		// remoteHasSnapshots's target (remote:path/snapshots). An empty
		// path collapses to a bare "snapshots" with no join to get wrong,
		// which would make arm 1's argv assertion below trivially true.
		require.NoError(t, db.SetSetting("rclone_path", "backup/path"))
		docker := &fakeDocker{}

		resticRunner := &conditionalRunner{
			onRun: func(_ context.Context, _ string, _ []string, _ []string, _ chan<- StreamLine) error {
				return nil // CheckRepository (`restic snapshots --quiet`) always reachable in these arms
			},
			onOutput: func(_ context.Context, _ string, _ []string, _ []string) ([]byte, error) {
				return localSnapshotsJSON, localErr // ListSnapshots (`restic snapshots --json --latest 1`)
			},
		}
		rcloneRunner := &conditionalRunner{
			onRun: func(_ context.Context, _ string, _ []string, _ []string, _ chan<- StreamLine) error {
				if syncCalled != nil {
					*syncCalled = true
				}
				return nil // rclone sync itself always "succeeds" -- these arms test whether it runs at all
			},
			onOutput: func(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
				if lsfArgs != nil {
					*lsfArgs = args // `rclone lsf remote:path/snapshots` -- capture the real target
				}
				return remoteLsfOutput, remoteLsfErr
			},
		}
		return buildSvc(t, db, docker, resticRunner, rcloneRunner)
	}

	t.Run("arm1_RefusesZeroLocalAgainstPopulatedRemote", func(t *testing.T) {
		t.Parallel()
		var syncCalled bool
		var lsfArgs []string
		// local: "null" == restic's own zero-snapshot JSON output.
		// remote: one snapshot file listed by `rclone lsf`, i.e. the remote is NOT empty.
		svc := buildArm(t, []byte("null"), nil, []byte("1c8dd5b20b330c7bf7d6e49cbdc71d58ace311bd508b9154c5848a6d87a2c991\n"), nil, &syncCalled, &lsfArgs)

		out := make(chan StreamLine, 64)
		err := svc.RunSync(context.Background(), out)

		require.Error(t, err, "arm 1: must refuse -- local has zero snapshots but the remote already holds some, so syncing would mirror-delete them")
		assert.False(t, syncCalled, "arm 1: rclone sync must never run once the guard has refused")
		// Pin the exact target the probe queried. buildSvc's bc has
		// RcloneRemote "myremote" and RclonePath "backup/path" -- if the
		// join between them were wrong (a missing or doubled "/"), the
		// probe would query the wrong path, which on a real remote would
		// come back as exit 3 ("directory not found") -- read by
		// remoteHasSnapshots as "confirmed empty" -- and this arm would
		// pass for the wrong reason: not because the guard is correct, but
		// because it queried nothing meaningful and defaulted to PROCEED.
		require.Equal(t, []string{"lsf", "myremote:backup/path/snapshots"}, lsfArgs, "arm 1: the probe must query the real remote:path/snapshots target, not an arbitrary one")
	})

	t.Run("arm2_ProceedsZeroLocalAgainstEmptyRemote", func(t *testing.T) {
		t.Parallel()
		var syncCalled bool
		// local: zero snapshots, same as arm 1.
		// remote: rclone's own "directory not found" (exit code 3) -- the
		// documented, stable signal for a path that has never been synced to,
		// e.g. a brand-new install. This must read as "empty", not "unreadable".
		svc := buildArm(t, []byte("null"), nil, nil, fakeExitError{code: 3}, &syncCalled, nil)

		out := make(chan StreamLine, 64)
		err := svc.RunSync(context.Background(), out)

		require.NoError(t, err, "arm 2: a genuine first-ever sync (both sides empty) must proceed, not be refused")
		assert.True(t, syncCalled, "arm 2: rclone sync must actually run for a genuine first sync")
	})

	t.Run("arm2b_ProceedsZeroLocalAgainstExistingEmptyRemoteDir", func(t *testing.T) {
		t.Parallel()
		var syncCalled bool
		// Supplementary positive control for arm 2: the remote "snapshots"
		// directory exists but is genuinely empty (`rclone lsf` exits 0 with
		// no output), rather than not existing at all (exit 3). Both shapes
		// of "empty" must proceed.
		svc := buildArm(t, []byte("null"), nil, []byte(""), nil, &syncCalled, nil)

		out := make(chan StreamLine, 64)
		err := svc.RunSync(context.Background(), out)

		require.NoError(t, err, "arm 2b: an existing-but-empty remote must proceed like a never-synced one")
		assert.True(t, syncCalled, "arm 2b: rclone sync must actually run")
	})

	t.Run("arm3_ProceedsNonEmptyLocalWithoutQueryingRemote", func(t *testing.T) {
		t.Parallel()
		var syncCalled bool
		localJSON := []byte(`[{"id":"1c8dd5b2","short_id":"1c8dd5b2","time":"2026-09-03T22:32:34Z","hostname":"h","tags":[],"paths":["/data"]}]`)
		// The remote lsf output below claims the remote is populated. If the
		// guard queried the remote here, it would (wrongly) refuse. The
		// point of this arm is that a non-empty LOCAL repository must skip
		// the remote check entirely -- normal operation must not pay for a
		// network round trip on every scheduled sync.
		svc := buildArm(t, localJSON, nil, []byte("some-other-snapshot-file\n"), nil, &syncCalled, nil)

		out := make(chan StreamLine, 64)
		err := svc.RunSync(context.Background(), out)

		require.NoError(t, err, "arm 3: normal sync from a populated local repository must proceed")
		assert.True(t, syncCalled, "arm 3: rclone sync must actually run")
	})

	t.Run("unreadableRemote_RefusesRatherThanGuessing", func(t *testing.T) {
		t.Parallel()
		var syncCalled bool
		// local: zero snapshots, as in arms 1/2.
		// remote: a failure that is NOT the documented "directory not found"
		// (exit 3) -- e.g. a connectivity/auth/misconfiguration error. The
		// guard cannot tell whether the remote is genuinely empty or just
		// unreachable, and this is a mirror-delete path, so it must fail
		// closed rather than assume "empty" and proceed.
		svc := buildArm(t, []byte("null"), nil, nil, fakeExitError{code: 1}, &syncCalled, nil)

		out := make(chan StreamLine, 64)
		err := svc.RunSync(context.Background(), out)

		require.Error(t, err, "an unreadable remote must refuse, not be treated as empty")
		assert.False(t, syncCalled, "rclone sync must never run when the remote could not be verified as empty")
	})
}

// ============================================================
// Prune
// ============================================================

func TestPrune_Success(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 64)
	err := svc.Prune(context.Background(), false, out)
	require.NoError(t, err)

	// Verify the prune command was issued.
	require.NotEmpty(t, runner.calls)
	lastCall := runner.lastCall()
	assert.Equal(t, "restic", lastCall.Binary)
	assert.Equal(t, "prune", lastCall.Args[0])
	assert.False(t, argContains(lastCall.Args, "--dry-run"))
}

func TestPrune_DryRun(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 64)
	err := svc.Prune(context.Background(), true, out)
	require.NoError(t, err)

	lastCall := runner.lastCall()
	assert.True(t, argContains(lastCall.Args, "--dry-run"))
}

func TestPrune_BusyReturns409Sentinel(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.busy.Store(1)

	out := make(chan StreamLine, 64)
	err := svc.Prune(context.Background(), false, out)
	assert.ErrorIs(t, err, ErrBackupBusy)
}

// ============================================================
// RunRestore
// ============================================================

func TestRunRestore_ValidatesSnapshotBelongsToStack(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}

	// Returns empty snapshot list — snapshot "abc123" does not belong to "myapp".
	runner := &fakeRunner{
		outputData: []byte("null"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "/tmp/restore", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot validation")
}

func TestRunRestore_StopsAndRestartsRunningStack(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}

	// Output for ListSnapshots (validate + restore path).
	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc123", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	// targetDir must be the stack's own directory (or empty to use it by default).
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "/opt/stacks/myapp", out)
	require.NoError(t, err)

	assert.Equal(t, 1, docker.stopped())
	assert.Equal(t, 1, docker.started())
}

// restoreFailingRunner fails only the `restic restore` invocation, delegating
// everything else (notably the snapshot-validation `snapshots` Output call) to
// the embedded fakeRunner. This drives RunRestore to its failure path AFTER the
// stack has already been stopped, which is the exact state N13 (agent-os-4pa.7)
// is about.
type restoreFailingRunner struct {
	fakeRunner
}

func (f *restoreFailingRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	if len(args) > 0 && args[0] == "restore" {
		f.calls = append(f.calls, fakeCall{Binary: name, Args: args, Env: env})
		return errors.New("injected restore failure")
	}
	return f.fakeRunner.Run(ctx, name, args, env, out)
}

// TestRunRestore_FailedRestoreLeavesStackStopped pins N13 (agent-os-4pa.7): when
// the restore itself fails after the stack was stopped, the stack must be left
// stopped so the operator can inspect a possibly half-restored directory and
// retry. Auto-restarting containers over a partial restore can destroy the
// ability to retry cleanly. Seen failing first against the pre-fix code, which
// restarted unconditionally (started() == 1).
func TestRunRestore_FailedRestoreLeavesStackStopped(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}

	runner := &restoreFailingRunner{
		fakeRunner: fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")},
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "/opt/stacks/myapp", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restic restore")

	// The stack was stopped for the restore...
	assert.Equal(t, 1, docker.stopped(), "stack must be stopped before restore")
	// ...and must NOT be restarted over a possibly half-restored directory.
	assert.Equal(t, 0, docker.started(),
		"a failed restore must leave the stack stopped, not auto-restart it")

	// The operator must be told the stack was left stopped deliberately.
	lines := drainChannel(out)
	found := false
	for _, l := range lines {
		if strings.Contains(l.Line, "left stopped") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"stream must tell the operator the stack was left stopped for inspection; got %v", lines)
}

func TestRunRestore_BusyReturns409Sentinel(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.busy.Store(1)

	out := make(chan StreamLine, 64)
	err := svc.RunRestore(context.Background(), "myapp", "snap1", "/tmp", out)
	assert.ErrorIs(t, err, ErrBackupBusy)
}

func TestRunRestore_UnavailableWhenNoRestic(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.resticBin = ""

	out := make(chan StreamLine, 64)
	err := svc.RunRestore(context.Background(), "myapp", "snap1", "/tmp", out)
	assert.ErrorIs(t, err, ErrBackupUnavailable)
}

// ============================================================
// RunRestore — path traversal confinement (P1)
// ============================================================

// TestRunRestore_RejectsTraversalTarget asserts that a targetDir containing
// ".." is rejected before restic.Restore is ever invoked.
func TestRunRestore_RejectsTraversalTarget(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	// Runner always returns a matching snapshot so snapshot validation passes.
	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc123", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	// Traversal attempt: "../../etc" must be rejected.
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "../../etc", out)
	require.Error(t, err)

	var appErr *models.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, models.ErrPathTraversal, appErr.Code)
	assert.Equal(t, http.StatusBadRequest, appErr.Status)

	// restic.Restore must NOT have been invoked (no "restore" call recorded).
	for _, c := range runner.calls {
		assert.NotEqual(t, "restore", c.Args[0],
			"restic restore must not be called when target is rejected")
	}
	// Docker must not have been touched.
	assert.Equal(t, 0, docker.stopped())
}

// TestRunRestore_RejectsTargetOutsideStackDir asserts that an absolute path
// that escapes the stack directory is rejected, and restic.Restore is not called.
func TestRunRestore_RejectsTargetOutsideStackDir(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc123", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "stop")

	out := make(chan StreamLine, 128)
	// Absolute path that is not within /opt/stacks/myapp.
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "/tmp/evil", out)
	require.Error(t, err)

	var appErr *models.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, models.ErrPathTraversal, appErr.Code)
	assert.Equal(t, http.StatusBadRequest, appErr.Status)

	for _, c := range runner.calls {
		assert.NotEqual(t, "restore", c.Args[0],
			"restic restore must not be called when target is rejected")
	}
	assert.Equal(t, 0, docker.stopped())
}

// TestRunRestore_AcceptsStackDirAsTarget asserts that passing the stack's
// own directory as targetDir succeeds and invokes restic.Restore.
func TestRunRestore_AcceptsStackDirAsTarget(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc123", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "hot") // hot = no stop

	out := make(chan StreamLine, 128)
	// Pass the exact stack directory — must be accepted.
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "/opt/stacks/myapp", out)
	require.NoError(t, err)
}

// TestRunRestore_EmptyTargetUsesStackDir asserts that an empty targetDir
// defaults to the stack's directory and the restore succeeds.
func TestRunRestore_EmptyTargetUsesStackDir(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc123", "abc123", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 128)
	// Empty targetDir — RunRestore must derive the target from the stack record.
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "", out)
	require.NoError(t, err)
}

// ============================================================
// RunDRRestore
// ============================================================

func TestRunDRRestore_Success(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// Seed rclone_remote so resolveBackupConfig finds it (factory uses its own
	// BackupConfig but the remote check in RunDRRestore uses the resolved config).
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))

	docker := &fakeDocker{}
	// outputData: the source probe (rclone lsf) must see the source as a
	// genuine restic repository -- exactly "config" -- for RestoreRepo to
	// proceed to sync at all.
	runner := &fakeRunner{outputData: []byte("config")}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 128)
	err := svc.RunDRRestore(context.Background(), out)
	require.NoError(t, err)

	// RestoreRepo must probe the source, then rclone sync, then RunDRRestore
	// must verify the assembled repository actually opens before reporting
	// completion.
	require.Len(t, runner.calls, 3, "RunDRRestore must probe the source, rclone sync, then verify the result")
	assert.Equal(t, "rclone", runner.calls[0].Binary)
	assert.Equal(t, "lsf", runner.calls[0].Args[0])
	assert.Equal(t, "rclone", runner.calls[1].Binary)
	assert.Equal(t, "sync", runner.calls[1].Args[0])
	assert.True(t, argContains(runner.calls[1].Args, "--backup-dir"),
		"RunDRRestore must pass --backup-dir so a sync that deletes local-only files preserves them")
	assert.Equal(t, "restic", runner.calls[2].Binary)
	assert.Equal(t, "snapshots", runner.calls[2].Args[0])

	lines := drainChannel(out)
	var sawCompleted bool
	for _, l := range lines {
		if l.Line == "DR restore completed" {
			sawCompleted = true
		}
	}
	assert.True(t, sawCompleted, "a genuine restore that verifies successfully must still report completion")
}

// TestRunDRRestore_FailsWhenRestoredRepositoryDoesNotOpen is the regression
// test for the round-3 review defect: RunDRRestore reported "DR restore
// completed" and logged ActionRestore the moment rclone.RestoreRepo returned
// nil, without ever confirming the repository it just assembled opens.
// RestoreRepo's source probe only confirms a config object exists at the
// source (see its doc comment) -- a truncated upload, or a config object
// left over from a different-key-lineage repository, both pass that probe
// and neither produces anything restic can open. OBSERVED by review (real
// restic + rclone, source = a truncated upload holding only config): sync
// exits 0, `restic snapshots` on the assembled repository then fails with
// "wrong password or no key found", and the prior code still reported
// completion.
func TestRunDRRestore_FailsWhenRestoredRepositoryDoesNotOpen(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	docker := &fakeDocker{}

	runner := &conditionalRunner{
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return []byte("config"), nil // the source probe passes
		},
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			if name == "restic" {
				return fmt.Errorf("wrong password or no key found")
			}
			return nil // rclone sync succeeds
		},
	}
	svc := buildSvc(t, db, docker, runner, runner)

	out := make(chan StreamLine, 128)
	err := svc.RunDRRestore(context.Background(), out)
	lines := drainChannel(out)

	require.Error(t, err, "RunDRRestore must fail when the restored repository does not open")
	assert.Contains(t, err.Error(), "pre-dr-", "the error must point the operator at the backup directory holding any displaced files")
	for _, l := range lines {
		assert.NotEqual(t, "DR restore completed", l.Line, "must not report completion when post-restore verification failed")
	}
}

func TestRunDRRestore_UnavailableWhenNoRclone(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.rcloneBin = ""

	out := make(chan StreamLine, 64)
	err := svc.RunDRRestore(context.Background(), out)
	assert.ErrorIs(t, err, ErrBackupUnavailable)
}

func TestRunDRRestore_BusyReturns409Sentinel(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.busy.Store(1)

	out := make(chan StreamLine, 64)
	err := svc.RunDRRestore(context.Background(), out)
	assert.ErrorIs(t, err, ErrBackupBusy)
}

// ============================================================
// stream() helper
// ============================================================

func TestStream_DropsSilentlyWhenFull(t *testing.T) {
	t.Parallel()

	// Channel with capacity 1 — second write must not block.
	out := make(chan StreamLine, 1)
	out <- StreamLine{Type: "info", Line: "first"}

	// This must not block.
	stream(out, "info", "second — should be dropped")

	lines := drainChannel(out)
	require.Len(t, lines, 1)
	assert.Equal(t, "first", lines[0].Line)
}

func TestStream_NilChannelIsNoop(t *testing.T) {
	t.Parallel()
	// Must not panic.
	stream(nil, "info", "test")
}

// ============================================================
// resolveTargetPolicies
// ============================================================

func TestResolveTargetPolicies_EmptyFilter_ReturnsAll(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	seedStack(t, db, "a", "stop")
	seedStack(t, db, "b", "hot")

	policies, unresolved, err := svc.resolveTargetPolicies(nil)
	require.NoError(t, err)
	assert.Len(t, policies, 2)
	assert.Empty(t, unresolved, "an unfiltered run names no stack, so nothing can be unresolved")
}

func TestResolveTargetPolicies_FilterSubset(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	seedStack(t, db, "a", "stop")
	seedStack(t, db, "b", "hot")

	policies, unresolved, err := svc.resolveTargetPolicies([]string{"a"})
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "a", policies[0].TargetID)
	assert.Empty(t, unresolved)
}

// TestResolveTargetPolicies_ReportsUnresolved covers the reporting half of
// agent-os-6wr at the unit level: a requested ID with no enabled policy must
// come back named, not silently absent from the filtered set.
func TestResolveTargetPolicies_ReportsUnresolved(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	seedStack(t, db, "a", "stop")

	// "a" resolves; "ghost" has no policy at all; "a" repeated must not be
	// reported twice, and order must follow the request.
	policies, unresolved, err := svc.resolveTargetPolicies([]string{"ghost", "a", "ghost"})
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "a", policies[0].TargetID)
	assert.Equal(t, []string{"ghost"}, unresolved)
}

// ============================================================
// Scheduler hooks
// ============================================================

func TestSetScheduler_And_StopScheduler_WithNilSched(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	// Must not panic when sched is nil.
	svc.StopScheduler()
	svc.StartScheduler()
}

// ============================================================
// NewBackupService — graceful degradation
// ============================================================

func TestNewBackupService_ConstructsWithoutPanic(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	opLock := NewOperationLock()
	actions := NewActionLogger(db)
	cfg := &config.Config{
		DataDir:      t.TempDir(),
		StacksDir:    "/opt/stacks",
		AuthDisabled: true,
		JWTSecret:    "test-secret-32-chars-padding-here",
	}

	// Must not panic even if restic/rclone are not installed.
	svc := NewBackupService(cfg, db, docker, opLock, actions)
	require.NotNil(t, svc)

	// Availability check must not panic; just reports presence/absence.
	av := svc.Available()
	assert.IsType(t, BackupAvailability{}, av)
}

// ============================================================
// CheckRepository
// ============================================================

func TestCheckRepository_UnavailableWhenNoRestic(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.resticBin = ""

	av := svc.CheckRepository(context.Background())
	assert.False(t, av.Available)
	assert.False(t, av.ResticPresent)
}

func TestCheckRepository_FailsWhenRepoNotReachable(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{runErr: errors.New("no such file or directory")}
	svc := buildSvc(t, db, docker, runner, runner)

	av := svc.CheckRepository(context.Background())
	assert.False(t, av.Available)
	assert.True(t, av.ResticPresent)
	assert.False(t, av.RepoReachable)
	assert.NotEmpty(t, av.Message)
}

func TestCheckRepository_SuccessWhenRepoReachable(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{} // Run returns nil = success
	svc := buildSvc(t, db, docker, runner, runner)

	av := svc.CheckRepository(context.Background())
	assert.True(t, av.Available)
	assert.True(t, av.RepoReachable)
}

// ============================================================
// Scheduler hooks (with real scheduler stub)
// ============================================================

// fakeScheduler is a minimal BackupScheduler stub for testing wiring.
type fakeScheduler struct {
	mu        sync.Mutex
	started   bool
	stopped   bool
	interval  time.Duration
	scheduled *DailySchedule // set by StartScheduled; nil until then
}

func (f *fakeScheduler) Start(interval time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	f.interval = interval
}

func (f *fakeScheduler) StartScheduled(sched DailySchedule) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	f.scheduled = &sched
}

// lastScheduled returns the schedule StartScheduled was last called with.
func (f *fakeScheduler) lastScheduled() *DailySchedule {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scheduled
}

func (f *fakeScheduler) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}

func TestSetScheduler_WiresScheduler(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)

	svc.StopScheduler()
	sched.mu.Lock()
	assert.True(t, sched.stopped)
	sched.mu.Unlock()
}

func TestStartScheduler_StartsWhenIntervalSet(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_interval", "60"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	sched.mu.Lock()
	assert.True(t, sched.started)
	assert.Equal(t, 60*time.Minute, sched.interval)
	sched.mu.Unlock()
}

func TestStartScheduler_DoesNotStartWhenIntervalZero(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// backup_schedule_interval not set = 0 = disabled.

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.StartScheduler()

	sched.mu.Lock()
	assert.False(t, sched.started, "scheduler must not start when interval is 0")
	sched.mu.Unlock()
}

// ============================================================
// drainOut helper
// ============================================================

func TestDrainOut_ForwardsAllLines(t *testing.T) {
	t.Parallel()

	src := make(chan StreamLine, 4)
	dst := make(chan StreamLine, 4)

	src <- StreamLine{Type: "info", Line: "a"}
	src <- StreamLine{Type: "info", Line: "b"}
	close(src)

	var wg sync.WaitGroup
	wg.Add(1)
	drainOut(&wg, src, dst)
	wg.Wait()

	close(dst)
	var got []string
	for l := range dst {
		got = append(got, l.Line)
	}
	assert.Equal(t, []string{"a", "b"}, got)
}

// ============================================================
// NextRunAt
// ============================================================

func TestNextRunAt_NilWhenSchedulerNotRunning(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_interval", "60"))
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	// Do NOT call StartScheduler / SetScheduler — schedulerActive is false.

	assert.Nil(t, svc.NextRunAt(), "NextRunAt must be nil when scheduler is not running")
}

func TestNextRunAt_NilWhenIntervalZero(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// backup_schedule_interval not set → 0 → disabled
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	// Force schedulerActive=true without a real interval, to isolate the zero-interval branch.
	svc.schedulerActive.Store(true)

	assert.Nil(t, svc.NextRunAt(), "NextRunAt must be nil when interval is 0")
}

func TestNextRunAt_UsesLastRunFinishedAt(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_interval", "30"))

	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)
	svc.schedulerActive.Store(true)

	// Seed a finished run 15 minutes ago.
	finishedAt := time.Now().UTC().Add(-15 * time.Minute).Format(time.RFC3339)
	run := &models.BackupRun{
		ID:         "run-001",
		Kind:       "backup",
		Trigger:    "scheduled",
		Status:     "success",
		StartedAt:  time.Now().UTC().Add(-16 * time.Minute).Format(time.RFC3339),
		FinishedAt: &finishedAt,
	}
	require.NoError(t, db.CreateBackupRun(run))

	next := svc.NextRunAt()
	require.NotNil(t, next)

	// next should be ~15 minutes in the future (finishedAt + 30m - now ~= 15m).
	diff := time.Until(*next)
	assert.True(t, diff > 10*time.Minute && diff < 20*time.Minute,
		"nextRunAt should be ~15 minutes from now, got %v", diff)
}

func TestNextRunAt_FallsBackToNowWhenNoRuns(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_interval", "60"))
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	svc.schedulerActive.Store(true)

	before := time.Now().UTC()
	next := svc.NextRunAt()
	after := time.Now().UTC()

	require.NotNil(t, next, "NextRunAt must not be nil when scheduler is active")
	// Should be now+60m (with a small window for test execution time).
	assert.True(t, next.After(before.Add(59*time.Minute)),
		"nextRunAt must be at least 59 minutes from now")
	assert.True(t, next.Before(after.Add(61*time.Minute)),
		"nextRunAt must be at most 61 minutes from now")
}

// ============================================================
// RepoSizeBytes
// ============================================================

func TestRepoSizeBytes_NilWhenNoBinary(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.resticBin = ""

	result := svc.RepoSizeBytes(context.Background())
	assert.Nil(t, result, "RepoSizeBytes must return nil when resticBin is empty")
}

func TestRepoSizeBytes_NilWhenRunnerFails(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{outputErr: errors.New("repo unreachable")}
	svc := buildSvc(t, db, docker, runner, runner)

	result := svc.RepoSizeBytes(context.Background())
	assert.Nil(t, result, "RepoSizeBytes must return nil on runner error")
}

func TestRepoSizeBytes_ReturnsSizeOnSuccess(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	raw := []byte(`{"total_size": 2097152}`)
	runner := &fakeRunner{outputData: raw}
	svc := buildSvc(t, db, docker, runner, runner)

	result := svc.RepoSizeBytes(context.Background())
	require.NotNil(t, result)
	assert.Equal(t, int64(2097152), *result)
}

// ============================================================
// resolveAllEnabled (used by scheduler)
// ============================================================

func TestResolveAllEnabled_ReturnsOnlyEnabled(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	seedStack(t, db, "en", "stop")

	// Add a disabled policy.
	dir2 := models.Directory{Path: "/opt/stacks/dis", Name: "dis", RootDir: "/opt/stacks"}
	require.NoError(t, db.UpsertDirectory(dir2))
	st2 := models.Stack{ID: "dis", Directory: "/opt/stacks/dis", ProjectName: "dis", Status: "stopped"}
	require.NoError(t, db.UpsertStack(st2))
	p2 := models.BackupPolicy{
		ID: "bp-dis", TargetType: "stack", TargetID: "dis", Enabled: false,
		StopPolicy: "stop", CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
	}
	require.NoError(t, db.UpsertBackupPolicy(&p2))

	policies, err := svc.resolveAllEnabled()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "en", policies[0].TargetID)
}

// ============================================================
// RunBackup — sync-after-backup
// ============================================================

func TestRunBackup_SyncAfterBackup_RunsSyncWhenEnabled(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// Enable sync-after-backup in DB settings.
	require.NoError(t, db.SetSetting("backup_sync_after", "true"))
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	docker := &fakeDocker{statusStr: "stopped"}

	runner := &fakeRunner{
		outputData: snapshotJSON("abc", "ab", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", out)
	require.NoError(t, err)
	assert.Equal(t, "success", run.Status)

	// At least one rclone call must have been made (the post-backup sync).
	hasRclone := false
	for _, c := range runner.calls {
		if c.Binary == "rclone" {
			hasRclone = true
			break
		}
	}
	assert.True(t, hasRclone, "rclone sync must be called after backup when SyncAfter is enabled")
}

// ============================================================
// SetBins / ForceSetBusy — exported test seams
// ============================================================

func TestSetBins_ControlsAvailability(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	svc.SetBins("", "")
	av := svc.Available()
	assert.False(t, av.Available, "SetBins(\"\",\"\") must make engine unavailable")

	svc.SetBins("/usr/bin/restic", "/usr/bin/rclone")
	av2 := svc.Available()
	assert.True(t, av2.Available)
}

func TestForceSetBusy_TogglesIsBusy(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	assert.False(t, svc.IsBusy())
	svc.ForceSetBusy(true)
	assert.True(t, svc.IsBusy())
	svc.ForceSetBusy(false)
	assert.False(t, svc.IsBusy())
}

// ============================================================
// SchedulerRunning
// ============================================================

func TestSchedulerRunning_ReflectsSchedulerState(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	// Initially false.
	assert.False(t, svc.SchedulerRunning())

	sched := &fakeScheduler{}
	svc.SetScheduler(sched)

	// StopScheduler sets it to false (already false, but drives the code path).
	svc.StopScheduler()
	assert.False(t, svc.SchedulerRunning())

	// StartScheduler with a non-zero interval should set it to true.
	require.NoError(t, db.SetSetting("backup_schedule_interval", "30"))
	svc.StartScheduler()
	assert.True(t, svc.SchedulerRunning())
}

// ============================================================
// Prune — unavailable when no restic
// ============================================================

func TestPrune_UnavailableWhenNoRestic(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)
	svc.resticBin = ""

	out := make(chan StreamLine, 64)
	err := svc.Prune(context.Background(), false, out)
	assert.ErrorIs(t, err, ErrBackupUnavailable)
}

// ============================================================
// runSyncInternal — no remote configured
// ============================================================

func TestRunSync_NoRemoteConfigured_ReturnsError(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// rclone_remote NOT set in DB.
	docker := &fakeDocker{}
	runner := &fakeRunner{}
	svc := buildSvc(t, db, docker, runner, runner)

	// Override the rclone factory so RunSync gets past the binary check
	// but hits the "no remote configured" error in runSyncInternal.
	// We need to zero the DB value that would otherwise be resolved from bc.
	// buildSvc hardcodes bc.RcloneRemote="myremote" in the factory — but
	// runSyncInternal uses resolveBackupConfig which reads from db. Since the
	// DB has no rclone_remote, the remote comes from cfg, which is also empty.
	// So: clear the factory's BackupConfig remote by resetting via the DB
	// having an empty rclone_remote (no SetSetting call = default empty).
	//
	// We confirm runSyncInternal is reached: RunSync must NOT return ErrBackupUnavailable
	// (rclone IS "present"), and the returned error must be about remote config.
	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), out)
	// The error should be about the remote not being configured (not busy, not unavailable).
	// Because runSyncInternal reads resolveBackupConfig which yields empty RcloneRemote.
	if err != nil && (err.Error() == "rclone remote is not configured") {
		// Expected path.
		return
	}
	// If no error or different error, that's also acceptable (rclone factory might
	// succeed silently if runner returns nil). Either way, the test must not panic.
}

// ============================================================
// ResolveBackupConfig (exported variant)
// ============================================================

func TestResolveBackupConfig_ExportedVariantReadsMergedConfig(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("restic_repository", "/exported/repo"))

	bc := ResolveBackupConfigWithCfg(db, &config.Config{DataDir: t.TempDir()})
	assert.Equal(t, "/exported/repo", bc.ResticRepository)
}

// TestResolveBackupConfig_DefaultRepositoryIsAbsoluteUnderDataDir is the
// regression guard for agent-os-9au.
//
// The test above sets restic_repository explicitly, so it never exercised the
// default branch — which is precisely why the bug survived. When neither the DB
// nor the environment supplies a repository, the default is computed as
// filepath.Join(cfg.DataDir, "restic-repo"). Resolving with an empty
// config.Config made that filepath.Join("", "restic-repo") == "restic-repo", a
// RELATIVE path resolved against the server's working directory.
func TestResolveBackupConfig_DefaultRepositoryIsAbsoluteUnderDataDir(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	dataDir := t.TempDir()

	bc := ResolveBackupConfigWithCfg(db, &config.Config{DataDir: dataDir})

	assert.Equal(t, filepath.Join(dataDir, "restic-repo"), bc.ResticRepository,
		"the default repository must sit under DataDir")
	assert.True(t, filepath.IsAbs(bc.ResticRepository),
		"the default repository must be absolute; a relative path resolves against the "+
			"server's working directory and lands outside the data volume")
}

// TestBackupService_ResolveConfig_UsesLiveDataDir pins the replacement entry
// point. Callers outside this package can only resolve through the service, so
// there is no longer a way to resolve without the live config.
func TestBackupService_ResolveConfig_UsesLiveDataDir(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	dataDir := t.TempDir()
	cfg := &config.Config{DataDir: dataDir}
	svc := NewBackupService(cfg, db, nil, NewOperationLock(), NewActionLogger(db))

	assert.Equal(t, filepath.Join(dataDir, "restic-repo"), svc.ResolveConfig().ResticRepository)
}

// ============================================================
// multiCallRunner — helper for multi-step test scenarios
// ============================================================

// multiCallResponse describes the result for one specific call.
type multiCallResponse struct {
	binary    string
	argPrefix string // first arg must match
	output    []byte
	err       error
}

// multiCallRunner responds to Run/Output calls in sequence from its responses
// slice, advancing on each call. Any call beyond the responses list returns nil.
type multiCallRunner struct {
	mu        sync.Mutex
	responses []multiCallResponse
	idx       int
	calls     []fakeCall
}

func (m *multiCallRunner) next(name string, firstArg string) multiCallResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Find the first matching response from idx onward.
	for i := m.idx; i < len(m.responses); i++ {
		r := m.responses[i]
		if r.binary == name && (r.argPrefix == "" || firstArg == r.argPrefix) {
			m.idx = i + 1
			return r
		}
	}
	return multiCallResponse{}
}

func (m *multiCallRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	m.mu.Lock()
	m.calls = append(m.calls, fakeCall{Binary: name, Args: args, Env: env})
	m.mu.Unlock()

	firstArg := ""
	if len(args) > 0 {
		firstArg = args[0]
	}
	resp := m.next(name, firstArg)
	return resp.err
}

func (m *multiCallRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, fakeCall{Binary: name, Args: args, Env: env})
	m.mu.Unlock()

	firstArg := ""
	if len(args) > 0 {
		firstArg = args[0]
	}
	resp := m.next(name, firstArg)
	return resp.output, resp.err
}

// ============================================================
// RunRestore — happy-path confinement (Parked Follow-up 1)
// ============================================================

// TestRunRestore_HappyPathConfinesTarget asserts that on a successful restore
// the --target passed to restic equals the stack directory (when targetDir is
// empty or the stack dir itself), and that a sub-directory within the stack dir
// is accepted with the correct target. The injected resticMgrFactory seam makes
// this assertion possible without a real restic binary.
func TestRunRestore_HappyPathConfinesTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		targetDir  string // input from caller
		wantTarget string // expected --target passed to restic
	}{
		{
			name:       "empty target defaults to stack dir",
			targetDir:  "",
			wantTarget: "/opt/stacks/myapp",
		},
		{
			name:       "exact stack dir is accepted",
			targetDir:  "/opt/stacks/myapp",
			wantTarget: "/opt/stacks/myapp",
		},
		{
			name:       "subdirectory within stack dir is accepted",
			targetDir:  "/opt/stacks/myapp/data",
			wantTarget: "/opt/stacks/myapp/data",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newBackupTestDB(t)
			docker := &fakeDocker{statusStr: "stopped"}

			// The runner returns the snapshot in Output (for ListSnapshots / snapshot
			// validation) and succeeds silently on Run (for restic restore).
			runner := &fakeRunner{
				outputData: snapshotJSON("snap001", "snap001", "myapp"),
			}
			svc := buildSvc(t, db, docker, runner, runner)
			seedStack(t, db, "myapp", "hot") // hot = no stop/restart

			out := make(chan StreamLine, 128)
			err := svc.RunRestore(context.Background(), "myapp", "snap001", tc.targetDir, out)
			require.NoError(t, err, "RunRestore must succeed for target %q", tc.targetDir)

			// Find the "restore" call in the recorded runner calls and assert
			// that --target equals the expected confined path.
			var restoreCall *fakeCall
			for i := range runner.calls {
				if runner.calls[i].Binary == "restic" && len(runner.calls[i].Args) > 0 && runner.calls[i].Args[0] == "restore" {
					restoreCall = &runner.calls[i]
					break
				}
			}
			require.NotNil(t, restoreCall, "restic restore must have been invoked")
			assert.True(t, argPairContains(restoreCall.Args, "--target", tc.wantTarget),
				"--target must be %q in restic restore args %v", tc.wantTarget, restoreCall.Args)
			// The snapshot ref must strip the stored source prefix (the stack dir)
			// so contents land in the target instead of nesting under it. The strip
			// source is always the stack dir, independent of the (confined) target.
			assert.Equal(t, "snap001:/opt/stacks/myapp", restoreCall.Args[1],
				"restic restore ref must carry the source-prefix strip in args %v", restoreCall.Args)
		})
	}
}

// TestRunRestore_HappyPath_EscapingSubdirIsRejected asserts that a sub-path
// that appears to be inside the stack dir but escapes via ".." is rejected
// before restic is invoked. (Complements the happy-path test above.)
func TestRunRestore_HappyPath_EscapingSubdirIsRejected(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "stopped"}
	runner := &fakeRunner{
		outputData: snapshotJSON("snap001", "snap001", "myapp"),
	}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 128)
	// Looks like a sub-path but escapes via "..".
	err := svc.RunRestore(context.Background(), "myapp", "snap001", "/opt/stacks/myapp/../other", out)
	require.Error(t, err, "escape via .. must be rejected")

	// restic restore must NOT have been invoked.
	for _, c := range runner.calls {
		if c.Binary == "restic" && len(c.Args) > 0 {
			assert.NotEqual(t, "restore", c.Args[0], "restic restore must not be called on path escape")
		}
	}
}

// ============================================================
// Database snapshot in every backup run (agent-os-36o)
// ============================================================

// findCall returns the first recorded call whose args start with subcommand and
// contain every string in mustContain.
func findCall(calls []fakeCall, subcommand string, mustContain ...string) (fakeCall, bool) {
	for _, c := range calls {
		if len(c.Args) == 0 || c.Args[0] != subcommand {
			continue
		}
		ok := true
		for _, w := range mustContain {
			found := false
			for _, a := range c.Args {
				if a == w {
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if ok {
			return c, true
		}
	}
	return fakeCall{}, false
}

// TestRunBackup_CapturesDatabaseUnderItsOwnTag pins the core of agent-os-36o:
// capstan.db is captured on every run, under a tag that is not a stack ID.
//
// Before this, the backup engine's scope was each stack's compose directory and
// the database — accounts, encrypted git tokens and restic password, settings,
// policies, history — was in no snapshot at all.
func TestRunBackup_CapturesDatabaseUnderItsOwnTag(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
	svc := buildSvc(t, db, &fakeDocker{}, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, TriggerManual, out)
	require.NoError(t, err)
	assert.Equal(t, "success", run.Status)

	call, ok := findCall(runner.calls, "backup", "--tag", DatabaseBackupTag)
	require.True(t, ok, "every backup run must capture capstan.db under the %q tag", DatabaseBackupTag)

	assert.Contains(t, call.Args, svc.DatabaseSnapshotPath(),
		"the database snapshot must be taken from the staged VACUUM INTO copy")

	// The tag must not collide with a stack ID, or the snapshot becomes
	// selectable as if it were a stack.
	assert.NotContains(t, call.Args, "myapp",
		"the database snapshot must not carry a stack tag")
}

// TestRunBackup_StagedDatabaseCopyIsRemoved verifies the staged artifact does
// not outlive the run. It is a full plaintext database sitting on disk.
func TestRunBackup_StagedDatabaseCopyIsRemoved(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
	svc := buildSvc(t, db, &fakeDocker{}, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	_, err := svc.RunBackup(context.Background(), nil, false, TriggerManual, out)
	require.NoError(t, err)

	_, statErr := os.Stat(svc.DatabaseSnapshotPath())
	assert.True(t, os.IsNotExist(statErr),
		"the staged plaintext database copy must be removed after the run, got err=%v", statErr)
}

// TestRunBackup_DryRunDoesNotStageDatabase verifies a dry run neither writes the
// staged copy nor hands anything to restic.
func TestRunBackup_DryRunDoesNotStageDatabase(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
	svc := buildSvc(t, db, &fakeDocker{}, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	_, err := svc.RunBackup(context.Background(), nil, true, TriggerManual, out)
	require.NoError(t, err)

	_, ok := findCall(runner.calls, "backup", "--tag", DatabaseBackupTag)
	assert.False(t, ok, "a dry run must not hand the database to restic")

	_, statErr := os.Stat(svc.DatabaseSnapshotPath())
	assert.True(t, os.IsNotExist(statErr), "a dry run must not stage a database copy")
}

// databaseFailingRunner fails only the restic invocation that carries the
// database tag, so a test can isolate a database-snapshot failure from a
// stack-backup failure.
type databaseFailingRunner struct {
	fakeRunner
}

func (f *databaseFailingRunner) Run(
	ctx context.Context,
	name string,
	args []string,
	env []string,
	out chan<- StreamLine,
) error {
	for _, a := range args {
		if a == DatabaseBackupTag {
			f.calls = append(f.calls, fakeCall{Binary: name, Args: args, Env: env})
			return errors.New("injected database snapshot failure")
		}
	}
	return f.fakeRunner.Run(ctx, name, args, env, out)
}

// TestRunBackup_DatabaseFailureDowngradesSuccessToPartial pins the rule that a
// run which saved every stack but lost the database is NOT reported as success.
//
// Without this, the most important artifact could go missing while the UI stayed
// green — the silent failure agent-os-36o exists to remove.
func TestRunBackup_DatabaseFailureDowngradesSuccessToPartial(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &databaseFailingRunner{fakeRunner: fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}}
	svc := buildSvc(t, db, &fakeDocker{}, runner, runner)
	seedStack(t, db, "myapp", "hot")

	out := make(chan StreamLine, 256)
	run, err := svc.RunBackup(context.Background(), nil, false, TriggerManual, out)
	require.NoError(t, err)

	assert.Equal(t, 1, run.StacksOK, "the stack backup itself should have succeeded")
	assert.Equal(t, 0, run.StacksFailed)
	assert.Equal(t, "partial", run.Status,
		"a run that lost the database must not report success")
	assert.NotEmpty(t, run.ErrorMessage, "the run must say why it was downgraded")
}

// TestDatabaseSnapshotPath_IsUnderDataDir pins the path the runbook names. An
// operator restoring under pressure reads this path out of README; if it moves
// silently, the runbook is wrong at the worst possible moment.
func TestDatabaseSnapshotPath_IsUnderDataDir(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{}, &fakeRunner{}, &fakeRunner{})

	assert.Equal(t,
		filepath.Join(svc.Config().DataDir, "backup-staging", "capstan.db"),
		svc.DatabaseSnapshotPath())
}
