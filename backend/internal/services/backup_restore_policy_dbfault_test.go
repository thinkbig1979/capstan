package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"

	_ "modernc.org/sqlite"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// agent-os-r1by made RunRestore refuse when the stack's backup policy could
// not be read, so that an unreadable "hot" policy was not silently replaced by
// "stop". agent-os-a1ye.2 removed the read altogether: the backup StopPolicy is
// a BACKUP setting ("Back up live") and a restore always stops the stack. The
// partial-fault fixture below is kept because it is now the guard that the
// read stays gone — with the policy table unreadable, a restore must proceed
// exactly as it does on a healthy database.
//
// THE FAULT FIXTURE IS NOT THE PACKAGE'S USUAL ONE, deliberately.
// closedDBWithSettings (backup_config_dbfault_test.go) cannot reach the restore
// path past resolveOrRefuse and GetStack, which both refuse on a closed
// database. Only a PARTIAL fault — settings and stacks readable, the policy
// table not — tells "restore ignores the policy" apart from "restore refuses
// on a database fault". That is what policyTableDroppedDB builds.

// The sentinel the fixture's narrowness self-control round-trips through the
// settings table. Deliberately not a restic key: nothing on the restore path
// reads it, so seeding it cannot influence what the tests below exercise.
const (
	narrowFaultSentinelKey   = "r1by_narrow_fault_sentinel"
	narrowFaultSentinelValue = "settings-table-still-readable"
)

// policyTableDroppedDB returns a healthy, migrated, ON-DISK database whose
// backup_policies table has been dropped through a second connection, so
// GetBackupPolicy's QueryRow(...).Scan returns a driver error ("no such table")
// rather than errdefs.ErrNotFound, while every other read — settings, directories,
// stacks — still succeeds.
//
// It must be on disk: newBackupTestDB uses ":memory:" with MaxOpenConns(1),
// where a second sql.Open gets an independent, empty database and the DROP
// would land nowhere the service can see.
func policyTableDroppedDB(t *testing.T, seed func(*database.DB)) *database.DB {
	t.Helper()

	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Seeded before the DROP so the narrowness half of the self-control below
	// has a known value to round-trip. Not a restic key: nothing in RunRestore
	// reads it, so it cannot change what the test under it exercises.
	if err := db.SetSetting(narrowFaultSentinelKey, narrowFaultSentinelValue); err != nil {
		t.Fatalf("seed narrowness sentinel: %v", err)
	}

	seed(db)

	// Same file, second connection. The DSN mirrors database.go:98 minus the
	// pragmas, which are connection-scoped and irrelevant to a single DDL
	// statement.
	raw, err := sql.Open("sqlite", dataDir+"/capstan.db")
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec("DROP TABLE backup_policies"); err != nil {
		t.Fatalf("drop backup_policies: %v", err)
	}

	// SELF-CONTROL, PERMANENT, TWO HALVES. Both are required and neither implies
	// the other.
	//
	// ARMED: the policy read must fail, and must NOT fail with errdefs.ErrNotFound,
	// or a green test below would be one that ran against a readable (or merely
	// empty) policy table while appearing to prove that a fault there is ignored.
	if _, pErr := db.GetBackupPolicy("myapp"); pErr == nil {
		t.Fatal("fixture is unarmed: GetBackupPolicy returned no error after DROP TABLE")
	} else if errors.Is(pErr, errdefs.ErrNotFound) {
		t.Fatalf("fixture is wrong: GetBackupPolicy returned errdefs.ErrNotFound, not a fault: %v", pErr)
	}

	// NARROW: stacks and settings must still READ. Without this the fixture
	// could widen into a whole-DB fault, and the test below would then fail on
	// resolveOrRefuse's or GetStack's refusal, which are other beads' behaviour.
	// Pinning the narrowness keeps this fixture pointed at the policy table alone.
	if _, sErr := db.GetStack("myapp"); sErr != nil {
		t.Fatalf("fixture is too wide: GetStack must still succeed, got %v", sErr)
	}
	// The settings half reads back a sentinel seeded above rather than probing
	// an arbitrary key: GetSetting returns the bare Scan error
	// (database/settings.go:14-20), so an ABSENT key is errdefs.ErrNotFound and a
	// "did it error" check could not tell a readable table from an unreadable
	// one. Round-tripping a known value can.
	got, gErr := db.GetSetting(narrowFaultSentinelKey)
	if gErr != nil {
		t.Fatalf("fixture is too wide: GetSetting must still succeed, got %v", gErr)
	}
	if got != narrowFaultSentinelValue {
		t.Fatalf("fixture is too wide: settings read back %q, want %q", got, narrowFaultSentinelValue)
	}

	return db
}

// bufferedSvc wires buildSvc's service to a private log buffer so the ERROR
// assertions below are independent of every other test in the package.
func bufferedSvc(
	t *testing.T,
	db *database.DB,
	docker *fakeDocker,
	runner commandRunner,
) (*BackupService, *bytes.Buffer) {
	t.Helper()
	svc := buildSvc(t, db, docker, runner, runner)
	var buf bytes.Buffer
	svc.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return svc, &buf
}

// seedStackOnly inserts the directory and stack record WITHOUT a backup policy,
// which is the absent-row control. seedStack always upserts a policy and so
// cannot express "no policy row".
func seedStackOnly(t *testing.T, db *database.DB, stackID string) {
	t.Helper()
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path:    "/opt/stacks/" + stackID,
		Name:    stackID,
		RootDir: "/opt/stacks",
	}))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          stackID,
		Directory:   "/opt/stacks/" + stackID,
		ProjectName: stackID,
		Status:      "running",
	}))
}

// TestRunRestore_UnreadablePolicyDoesNotMatter inverts agent-os-r1by's refusal
// test. A restore no longer reads the backup policy, so a policy table that
// will not read cannot refuse, delay or change it: the running stack is
// stopped, restored and restarted, and no ERROR is logged. Re-adding a policy
// read to the restore path turns this red.
func TestRunRestore_UnreadablePolicyDoesNotMatter(t *testing.T) {
	t.Parallel()

	db := policyTableDroppedDB(t, func(db *database.DB) {
		seedStackOnly(t, db, "myapp")
		require.NoError(t, db.UpsertBackupPolicy(&models.BackupPolicy{
			ID:         "bp-myapp",
			TargetType: "stack",
			TargetID:   "myapp",
			Enabled:    true,
			StopPolicy: "hot",
			CreatedAt:  time.Now().Format(time.RFC3339),
			UpdatedAt:  time.Now().Format(time.RFC3339),
		}))
	})

	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc, logBuf := bufferedSvc(t, db, docker, runner)

	out := make(chan StreamLine, 128)
	require.NoError(t, svc.RunRestore(context.Background(), "myapp", "abc123", out),
		"an unreadable backup policy must not refuse a restore that never reads it")

	assert.Equal(t, 1, docker.stopped(), "the restore stops the running stack")
	assert.Equal(t, 1, docker.started(), "and restarts it after the successful restore")

	logged := logBuf.String()
	assert.NotContains(t, logged, "level=ERROR", "nothing on the restore path reads the policy, so nothing faults")
	assert.NotContains(t, logged, "cause=")
}

// stopOrderRunner records, at the moment restic restore runs, how many times
// the stack had been stopped and started. stopped() == 1 after the call says a
// stop happened; only this snapshot says it happened BEFORE the restore wrote
// over the directory.
type stopOrderRunner struct {
	fakeRunner
	docker          *fakeDocker
	restoreCalls    int
	stopsAtRestore  int
	startsAtRestore int
}

func (r *stopOrderRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	if len(args) > 0 && args[0] == "restore" {
		r.restoreCalls++
		r.stopsAtRestore = r.docker.stopped()
		r.startsAtRestore = r.docker.started()
	}
	return r.fakeRunner.Run(ctx, name, args, env, out)
}

// TestRunRestore_HotPolicyStillStopsRunningStack pins agent-os-a1ye.2: the
// backup StopPolicy is a BACKUP setting ("Back up live"), and restore must not
// read it. A running stack whose stored policy is "hot" is stopped before the
// snapshot is written over its directory, and restarted after a successful
// restore. Pre-fix, the "hot" policy was applied to the restore and the stack
// stayed up underneath it while the confirm dialog promised a stop.
func TestRunRestore_HotPolicyStillStopsRunningStack(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	seedStack(t, db, "myapp", "hot")

	stored, err := db.GetBackupPolicy("myapp")
	require.NoError(t, err)
	require.Equal(t, "hot", stored.StopPolicy, "the fixture must actually store the hot policy")

	docker := &fakeDocker{statusStr: "running"}
	runner := &stopOrderRunner{
		fakeRunner: fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")},
		docker:     docker,
	}
	svc, logBuf := bufferedSvc(t, db, docker, runner)

	out := make(chan StreamLine, 128)
	require.NoError(t, svc.RunRestore(context.Background(), "myapp", "abc123", out))

	assert.Equal(t, 1, docker.stopped(), `a stored "hot" backup policy must not keep the stack running through a restore`)
	require.Equal(t, 1, runner.restoreCalls, "restic restore must have run exactly once")
	assert.Equal(t, 1, runner.stopsAtRestore, "the stop must happen BEFORE restic restore writes the directory")
	assert.Equal(t, 0, runner.startsAtRestore, "nothing may be restarted before the restore has run")
	assert.Equal(t, 1, docker.started(), "a stack that was running is restarted after a successful restore")
	assert.NotContains(t, logBuf.String(), "level=ERROR")
}

// TestRunRestore_HealthyDBNoPolicyRowKeepsStopDefault: a stack with NO policy
// row is stopped and restarted like any other, and logs no ERROR.
func TestRunRestore_HealthyDBNoPolicyRowKeepsStopDefault(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	seedStackOnly(t, db, "myapp")

	_, pErr := db.GetBackupPolicy("myapp")
	require.ErrorIs(t, pErr, errdefs.ErrNotFound,
		"this control is only meaningful if the absent row really is errdefs.ErrNotFound")

	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc, logBuf := bufferedSvc(t, db, docker, runner)

	out := make(chan StreamLine, 128)
	require.NoError(t, svc.RunRestore(context.Background(), "myapp", "abc123", out))

	assert.Equal(t, 1, docker.stopped(), "a stack with no policy row is stopped before the restore")
	assert.Equal(t, 1, docker.started(), "a running stack that was stopped is restarted after a successful restore")

	logged := logBuf.String()
	assert.NotContains(t, logged, "level=ERROR",
		"an absent policy row is not a fault and must not be logged as one")
	assert.False(t, strings.Contains(logged, "cause="),
		"no cause= line may be emitted for an absent row")
}
