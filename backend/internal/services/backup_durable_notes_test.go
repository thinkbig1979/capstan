package services

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// These tests read the run and its items back from the database with NO stream
// client attached (out == nil). That is the case the stream-only messages were
// lost in: stream() returns immediately on a nil channel, so anything an
// operator must see after the fact has to be in the durable record.

// readRun returns the stored run and its items for runID.
func readRun(t *testing.T, db *database.DB, runID string) (*models.BackupRun, []models.BackupRunItem) {
	t.Helper()
	stored, err := db.GetBackupRunByID(runID)
	require.NoError(t, err)
	items, err := db.GetBackupRunItems(runID)
	require.NoError(t, err)
	return stored, items
}

// TestRunBackup_DefensiveRestartOutcomeIsDurable pins agent-os-5gou: a stack
// the backup stopped and could not fully restart used to be recorded as a
// plain green success, the restart outcome living only in a log line and a
// stream line.
func TestRunBackup_DefensiveRestartOutcomeIsDurable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		docker        *fakeDocker
		wantRunStatus string
		wantNote      string // "" = no note on the item or the run
	}{
		{
			name:          "restart failed",
			docker:        &fakeDocker{statusStr: "running", startErr: errors.New("port 80 already allocated")},
			wantRunStatus: "partial",
			wantNote:      "restart after backup failed: port 80 already allocated",
		},
		{
			name:          "restart partially succeeded",
			docker:        &fakeDocker{statusStr: "running", startPartial: "1 of 2 containers running"},
			wantRunStatus: "partial",
			wantNote:      "restart after backup partially succeeded: 1 of 2 containers running",
		},
		{
			// Control: a clean restart stays a clean success with no message.
			name:          "clean restart",
			docker:        &fakeDocker{statusStr: "running"},
			wantRunStatus: "success",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newBackupTestDB(t)
			runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
			svc := buildSvc(t, db, tc.docker, runner, runner)
			seedStack(t, db, "myapp", "stop")

			run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
			require.NoError(t, err)
			require.Equal(t, 1, tc.docker.started(), "the stack must have been restarted once")

			stored, items := readRun(t, db, run.ID)
			require.Len(t, items, 1)
			// The backup itself succeeded: the snapshot is good, so the item
			// stays success. The restart outcome rides on it as a message.
			assert.Equal(t, "success", items[0].Status)
			assert.Equal(t, tc.wantNote, items[0].ErrorMessage)
			assert.Equal(t, 1, stored.StacksOK)
			assert.Equal(t, 0, stored.StacksFailed)

			assert.Equal(t, tc.wantRunStatus, stored.Status)
			if tc.wantNote == "" {
				assert.Empty(t, stored.ErrorMessage)
			} else {
				assert.Equal(t, "stack myapp: "+tc.wantNote, stored.ErrorMessage)
			}
		})
	}
}

// TestWithRunNotes_PrimaryReasonComesFirst pins the combine rule: a note can
// never displace the run's primary reason, notes keep their order, repeats are
// dropped and at most maxRunNotes are listed.
func TestWithRunNotes_PrimaryReasonComesFirst(t *testing.T) {
	t.Parallel()

	notes := []string{"n1", "n2", "n2", "n3", "n4", "n5", "n6"}
	got := withRunNotes("database snapshot failed; stack backups succeeded", notes)
	assert.Equal(t,
		"database snapshot failed; stack backups succeeded; n1; n2; n3; n4; n5; and 1 more (see per-stack details)",
		got)

	assert.Equal(t, "n1; n2", withRunNotes("", []string{"n1", "n2"}), "no primary: notes alone")
	assert.Equal(t, "primary", withRunNotes("primary", nil), "no notes: primary unchanged")
	assert.Equal(t, "", withRunNotes("", nil))
	assert.False(t, strings.HasPrefix(withRunNotes("p", []string{"p", "x"}), "p; p"),
		"a note equal to the primary reason is not repeated")
}

// uwfuRunner routes restic/rclone by subcommand so one step can fail while the
// rest succeed. Only calls for the stack under test (tagged uwfuStack) fail:
// the database snapshot shares "restic backup" and must keep succeeding.
type uwfuRunner struct {
	mu sync.Mutex

	failBackup    bool // restic backup --tag uwfuStack
	failLs        bool // restic ls (Verify)
	failForget    bool // restic forget (retention)
	failSync      bool // restic snapshots --quiet: the sync's repository preflight
	failSnapsFrom int  // fail the Nth and later `restic snapshots --tag uwfuStack` (1-based); 0 = never

	snapCalls int
}

const uwfuStack = "uwfu-app"

func (r *uwfuRunner) Run(_ context.Context, name string, args []string, _ []string, _ chan<- StreamLine) error {
	if len(args) == 0 {
		return errors.New("uwfu runner: empty argv")
	}
	forStack := slices.Contains(args, uwfuStack)
	switch {
	// Failing the preflight rather than `rclone sync` itself reaches the same
	// "post-backup sync failed" branch without RcloneManager.Sync's 30s/60s
	// retry backoff.
	case args[0] == "snapshots" && r.failSync:
		return errors.New("repository unreachable")
	case args[0] == "backup" && forStack && r.failBackup:
		return errors.New("repository locked")
	case args[0] == "ls" && r.failLs:
		return errors.New("pack file missing")
	case args[0] == "forget" && r.failForget:
		return errors.New("forget refused")
	}
	return nil
}

func (r *uwfuRunner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	if len(args) > 0 && args[0] == "snapshots" && slices.Contains(args, uwfuStack) {
		r.mu.Lock()
		r.snapCalls++
		n := r.snapCalls
		r.mu.Unlock()
		if r.failSnapsFrom > 0 && n >= r.failSnapsFrom {
			return nil, errors.New("snapshots listing timed out")
		}
	}
	return snapshotJSON("abc123", "abc", uwfuStack), nil
}

// TestRunBackup_StackReasonsAreDurable pins agent-os-uwfu: a stack's failure
// reason and its non-fatal warnings used to reach only the stream (and, for a
// failure before the backup started, no run item was written at all).
func TestRunBackup_StackReasonsAreDurable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		runner         *uwfuRunner
		docker         *fakeDocker
		wantItemStatus string
		wantItemMsg    string
		wantRunStatus  string
		wantOK         int
		wantFailed     int
	}{
		{
			// Early return before the backup: used to write no item at all.
			name:           "stop failed",
			runner:         &uwfuRunner{},
			docker:         &fakeDocker{statusStr: "running", stopErr: errors.New("daemon timeout")},
			wantItemStatus: "failed",
			wantItemMsg:    "stop stack: daemon timeout",
			wantRunStatus:  "failed",
			wantFailed:     1,
		},
		{
			name:           "restic backup failed",
			runner:         &uwfuRunner{failBackup: true},
			docker:         &fakeDocker{statusStr: "running"},
			wantItemStatus: "failed",
			wantItemMsg:    "restic backup: repository locked",
			wantRunStatus:  "failed",
			wantFailed:     1,
		},
		{
			name:           "verify warning",
			runner:         &uwfuRunner{failLs: true},
			docker:         &fakeDocker{statusStr: "running"},
			wantItemStatus: "success",
			wantItemMsg:    "verify warning: pack file missing",
			wantRunStatus:  "success",
			wantOK:         1,
		},
		{
			name:           "retention warning",
			runner:         &uwfuRunner{failForget: true},
			docker:         &fakeDocker{statusStr: "running"},
			wantItemStatus: "success",
			wantItemMsg:    "retention warning: forget refused",
			wantRunStatus:  "success",
			wantOK:         1,
		},
		{
			// Call 1 is Verify's own listing, call 2 the snapshot-id read-back.
			name:           "snapshot id unavailable",
			runner:         &uwfuRunner{failSnapsFrom: 2},
			docker:         &fakeDocker{statusStr: "running"},
			wantItemStatus: "success",
			wantItemMsg:    "snapshot id unavailable: list snapshots: snapshots listing timed out",
			wantRunStatus:  "success",
			wantOK:         1,
		},
		{
			// Control: a clean backup carries no message.
			name:           "clean",
			runner:         &uwfuRunner{},
			docker:         &fakeDocker{statusStr: "running"},
			wantItemStatus: "success",
			wantRunStatus:  "success",
			wantOK:         1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newBackupTestDB(t)
			svc := buildSvc(t, db, tc.docker, tc.runner, tc.runner)
			seedStack(t, db, uwfuStack, "stop")

			run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
			require.NoError(t, err)

			stored, items := readRun(t, db, run.ID)
			require.Len(t, items, 1, "exactly one item per stack, whatever path it took")
			assert.Equal(t, tc.wantItemStatus, items[0].Status)
			assert.Equal(t, tc.wantItemMsg, items[0].ErrorMessage)

			assert.Equal(t, tc.wantRunStatus, stored.Status)
			assert.Equal(t, tc.wantOK, stored.StacksOK)
			assert.Equal(t, tc.wantFailed, stored.StacksFailed, "an early-return item must not double-count")
			if tc.wantFailed > 0 {
				// All stacks failed: the run names the first failure, as before.
				assert.Equal(t, tc.wantItemMsg, stored.ErrorMessage)
			} else {
				// Warnings stay on the item; they do not change the run.
				assert.Empty(t, stored.ErrorMessage)
			}
		})
	}
}

// TestRunBackup_PostBackupSyncFailureIsDurable pins the run-level half of
// agent-os-uwfu: the sync runs after the run row is final, and its failure
// used to reach only the stream.
func TestRunBackup_PostBackupSyncFailureIsDurable(t *testing.T) {
	t.Parallel()

	for _, failSync := range []bool{true, false} {
		t.Run(map[bool]string{true: "sync failed", false: "sync ok (control)"}[failSync], func(t *testing.T) {
			db := newBackupTestDB(t)
			require.NoError(t, db.SetSetting("backup_sync_after", "true"))
			require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
			runner := &uwfuRunner{failSync: failSync}
			svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)
			seedStack(t, db, uwfuStack, "hot")

			run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
			require.NoError(t, err)

			stored, _ := readRun(t, db, run.ID)
			if failSync {
				assert.Equal(t, "partial", stored.Status)
				assert.Contains(t, stored.ErrorMessage, "post-backup sync failed: ")
				assert.Contains(t, stored.ErrorMessage, "repository unreachable")
				assert.Equal(t, stored.Status, run.Status, "the returned run matches the stored one")
			} else {
				assert.Equal(t, "success", stored.Status)
				assert.Empty(t, stored.ErrorMessage)
			}
		})
	}
}

// TestRunBackup_SyncNoteFollowsPrimaryReason: a sync failure on a run that
// already has a reason is appended after it, never in its place.
func TestRunBackup_SyncNoteFollowsPrimaryReason(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_sync_after", "true"))
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	runner := &uwfuRunner{failSync: true}
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)
	seedStack(t, db, uwfuStack, "hot")

	// A requested stack with no policy gives the run a primary reason.
	run, err := svc.RunBackup(context.Background(), []string{uwfuStack, "no-policy"}, false, "manual", nil)
	require.NoError(t, err)

	stored, _ := readRun(t, db, run.ID)
	assert.Equal(t, "partial", stored.Status)
	assert.True(t, strings.HasPrefix(stored.ErrorMessage,
		"no enabled backup policy for requested stack(s): no-policy; post-backup sync failed: "),
		"got %q", stored.ErrorMessage)
}

// unprovenNote is the substring of the durable unproven-premise notice.
const unprovenNote = "on an unproven premise"

// TestRunBackup_UnprovenRestartIsDurable pins the backup half of
// agent-os-gokn: restarting a stack whose prior state could not be read was
// announced only in a log line and a stream line.
func TestRunBackup_UnprovenRestartIsDurable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		docker *fakeDocker
		want   bool
	}{
		{"status unreadable", &fakeDocker{statusErr: errors.New("docker compose ps failed")}, true},
		{"known running (control)", &fakeDocker{statusStr: "running"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newBackupTestDB(t)
			runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc", "myapp")}
			svc := buildSvc(t, db, tc.docker, runner, runner)
			seedStack(t, db, "myapp", "stop")

			run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
			require.NoError(t, err)
			require.Equal(t, 1, tc.docker.started())

			stored, items := readRun(t, db, run.ID)
			require.Len(t, items, 1)
			assert.Equal(t, "success", items[0].Status)
			assert.Equal(t, "success", stored.Status, "a warning does not change the run status")
			if tc.want {
				assert.Equal(t,
					"restarted after backup on an unproven premise: docker status could not be read beforehand, "+
						"so whether this stack was running is unknown; if it was stopped deliberately, stop it again",
					items[0].ErrorMessage)
			} else {
				assert.Empty(t, items[0].ErrorMessage)
			}
		})
	}
}

// TestLaunchRestore_UnprovenRestartIsInTheRunRecord pins the restore half of
// agent-os-gokn. A restore run has no items, so the notice goes on the run.
// No WebSocket client ever attaches here, which is the case the stream-only
// notice was lost in.
func TestLaunchRestore_UnprovenRestartIsInTheRunRecord(t *testing.T) {
	for _, tc := range []struct {
		name       string
		docker     *fakeDocker
		wantStatus string
		wantPrefix string // "" = no message at all
	}{
		{
			name:       "status unreadable",
			docker:     &fakeDocker{statusErr: errors.New("docker compose ps failed")},
			wantStatus: "success",
			wantPrefix: "restarted after restore on an unproven premise: ",
		},
		{
			// The restart outcome stays the primary reason; the notice follows it.
			name:       "status unreadable and partial restart",
			docker:     &fakeDocker{statusErr: errors.New("docker compose ps failed"), startPartial: "1 of 3 containers not running"},
			wantStatus: "partial",
			wantPrefix: "restore completed, but restarting stack myapp partially succeeded: 1 of 3 containers not running; " +
				"restarted after restore on an unproven premise: ",
		},
		{
			name:       "known running (control)",
			docker:     &fakeDocker{statusStr: "running"},
			wantStatus: "success",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newBackupTestDB(t)
			runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
			svc := buildSvc(t, db, tc.docker, runner, runner)
			seedStack(t, db, "myapp", "stop")
			reg := NewBackupRunnerRegistry(db, svc, slog.Default())

			runID, err := reg.LaunchRestore("myapp", "abc123", "/opt/stacks/myapp")
			require.NoError(t, err)
			reg.Stop()

			stored, err := db.GetBackupRunByID(runID)
			require.NoError(t, err)
			require.Equal(t, 1, tc.docker.started())
			assert.Equal(t, tc.wantStatus, stored.Status)
			if tc.wantPrefix == "" {
				assert.Empty(t, stored.ErrorMessage)
				return
			}
			assert.True(t, strings.HasPrefix(stored.ErrorMessage, tc.wantPrefix), "got %q", stored.ErrorMessage)
			assert.Contains(t, stored.ErrorMessage, unprovenNote)
		})
	}
}
