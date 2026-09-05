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
)

// agent-os-r1by. RunRestore read the stack's stop policy with a softened error:
//
//	stopPolicy := "stop"
//	if policy, pErr := s.db.GetBackupPolicy(stackID); pErr == nil && policy != nil {
//	    stopPolicy = policy.StopPolicy
//	}
//
// The default is assigned BEFORE the getter and the read only ever OVERWRITES
// it, so a fault leaves "stop" standing and the stack IS stopped. What was
// silently discarded is a configured "hot" policy: the operator asked for the
// stack to stay up through the restore and it went down anyway, with nothing
// logged and no way to tell the outage from a policy they set themselves.
//
// THE FAULT FIXTURE IS NOT THE PACKAGE'S USUAL ONE, deliberately.
// closedDBWithSettings (backup_config_dbfault_test.go:57) cannot reach this
// site at all: a closed database trips resolveOrRefuse at backup.go:1108, and
// GetStack at backup.go:1121 would trip next, so control never arrives at the
// policy read. TESTED, not inferred: driving RunRestore with
// closedDBWithSettings returned
//
//	read backup setting "restic_repository": sql: database is closed
//
// with stopped=0, started=0 and only resolveOrRefuse's own ERROR line — the
// policy read never ran. Only a PARTIAL fault — settings and stacks readable,
// the policy table not — exercises it. That is what policyTableDroppedDB
// builds. This is method gap (i) from agent-os-l42o's close reason
// ("source-sweeping is not test-sweeping") recurring one bead later: the site
// was converted, and the package's existing fault instrument could not drive
// an error into it.

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
// rather than sql.ErrNoRows, while every other read — settings, directories,
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
	// ARMED: the policy read must fail, and must NOT fail with sql.ErrNoRows, or
	// a green test below would be one that quietly exercised today's absent-row
	// path — the arm the fix deliberately leaves alone — while appearing to
	// exercise the fault arm.
	if _, pErr := db.GetBackupPolicy("myapp"); pErr == nil {
		t.Fatal("fixture is unarmed: GetBackupPolicy returned no error after DROP TABLE")
	} else if errors.Is(pErr, sql.ErrNoRows) {
		t.Fatalf("fixture is wrong: GetBackupPolicy returned sql.ErrNoRows, not a fault: %v", pErr)
	}

	// NARROW: stacks and settings must still READ. Without this the fixture
	// could widen into a whole-DB fault and the test would keep passing while
	// testing something else entirely — resolveOrRefuse's refusal at
	// backup.go:1108, or GetStack's at :1121, both of which fire BEFORE the
	// policy read and are other beads' behaviour, not this one's. Pinning the
	// narrowness is what keeps this test pointed at the site it names.
	if _, sErr := db.GetStack("myapp"); sErr != nil {
		t.Fatalf("fixture is too wide: GetStack must still succeed, got %v", sErr)
	}
	// The settings half reads back a sentinel seeded above rather than probing
	// an arbitrary key: GetSetting returns the bare Scan error
	// (database/settings.go:14-20), so an ABSENT key is sql.ErrNoRows and a
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

// TestRunRestore_UnreadablePolicy_RefusesRatherThanSilentlyStopping is the
// FAILING-FIRST arm. A stack configured "hot" is restored behind a policy table
// that will not read. Pre-fix the read is softened, "stop" stands, StopVerified
// is called and RunRestore returns nil — a service interruption the operator
// explicitly configured against, with nothing logged.
func TestRunRestore_UnreadablePolicy_RefusesRatherThanSilentlyStopping(t *testing.T) {
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
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "", out)

	// THE BEHAVIOURAL ASSERTION COMES FIRST, DELIBERATELY. The defect is a stack
	// that gets stopped against the operator's configured "hot" policy, so that
	// is what the red output has to NAME. Ordered the other way round, the
	// leading require.Error short-circuits the test on the pre-fix code and the
	// failure reads only "an error is expected but got nil" — true, but no
	// evidence at all that the stack was wrongly stopped. Both assertions here
	// are non-fatal so a single run reports the behaviour AND the refusal.
	assert.Equal(t, 0, docker.stopped(),
		"the stack must not be stopped on a policy the operator may have configured hot")
	assert.Equal(t, 0, docker.started())

	require.Error(t, err, "an unreadable stop policy must refuse the restore, not fall back to \"stop\"")
	assert.Contains(t, err.Error(), "backup policy",
		"the refusal must name the policy read as the cause")

	logged := logBuf.String()
	assert.Contains(t, logged, "level=ERROR", "the fault must be logged at ERROR")
	assert.Contains(t, logged, "cause=", "the ERROR line must carry cause=")
}

// TestRunRestore_HealthyDBHotPolicyApplied is CONTROL 1: healthy database, a
// stored non-default policy, applied byte-for-byte. Without this the test above
// would pass equally on code that refused every restore.
func TestRunRestore_HealthyDBHotPolicyApplied(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	seedStack(t, db, "myapp", "hot")

	stored, err := db.GetBackupPolicy("myapp")
	require.NoError(t, err)
	require.Equal(t, "hot", stored.StopPolicy, "the fixture must actually store the non-default policy")

	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc, logBuf := bufferedSvc(t, db, docker, runner)

	out := make(chan StreamLine, 128)
	require.NoError(t, svc.RunRestore(context.Background(), "myapp", "abc123", "", out))

	assert.Equal(t, 0, docker.stopped(), `a stored "hot" policy must leave the stack running`)
	assert.Equal(t, 0, docker.started(), "nothing was stopped, so nothing is restarted")
	assert.NotContains(t, logBuf.String(), "level=ERROR")
}

// TestRunRestore_HealthyDBNoPolicyRowKeepsStopDefault is CONTROL 2: a healthy
// database with NO policy row keeps today's "stop" default and logs no ERROR.
// This is the sql.ErrNoRows arm — the half the fix must leave untouched.
func TestRunRestore_HealthyDBNoPolicyRowKeepsStopDefault(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	seedStackOnly(t, db, "myapp")

	_, pErr := db.GetBackupPolicy("myapp")
	require.ErrorIs(t, pErr, sql.ErrNoRows,
		"this control is only meaningful if the absent row really is sql.ErrNoRows")

	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc, logBuf := bufferedSvc(t, db, docker, runner)

	out := make(chan StreamLine, 128)
	require.NoError(t, svc.RunRestore(context.Background(), "myapp", "abc123", "", out))

	assert.Equal(t, 1, docker.stopped(), `no policy row must keep the conservative "stop" default`)
	assert.Equal(t, 1, docker.started(), "a running stack that was stopped is restarted after a successful restore")

	logged := logBuf.String()
	assert.NotContains(t, logged, "level=ERROR",
		"an absent policy row is not a fault and must not be logged as one")
	assert.False(t, strings.Contains(logged, "cause="),
		"no cause= line may be emitted for an absent row")
}
