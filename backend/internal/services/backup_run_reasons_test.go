package services

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunBackup_EveryRunReasonIsDurable pins agent-os-bscs: the run-level
// reasons used to be first-wins, so a database snapshot failure or unresolved
// stacks were dropped whenever the run already had a reason (or, for the
// database, whenever the run was not otherwise a success). No stream client is
// attached (out == nil), so the stored run is the only place they can be.
func TestRunBackup_EveryRunReasonIsDurable(t *testing.T) {
	t.Parallel()

	const dbReason = "database snapshot failed: restic backup database: database is locked"
	const unresolvedReason = "no enabled backup policy for requested stack(s): no-policy"
	const stackReason = "restic backup: repository locked"

	tests := []struct {
		name       string
		runner     *uwfuRunner
		otherStack bool     // seed a second stack that backs up fine
		requested  []string // nil = all enabled
		wantStatus string
		wantMsg    string
	}{
		{
			// Used to be "partial" with an EMPTY error_message.
			name:       "one stack failed and the database failed",
			runner:     &uwfuRunner{failBackup: true, failDB: true},
			otherStack: true,
			wantStatus: "partial",
			wantMsg:    dbReason,
		},
		{
			// Used to keep only the stack failure.
			name:       "all stacks failed and a requested stack is unresolved",
			runner:     &uwfuRunner{failBackup: true},
			requested:  []string{uwfuStack, "no-policy"},
			wantStatus: "failed",
			wantMsg:    stackReason + "; " + unresolvedReason,
		},
		{
			// Used to keep only the stack failure.
			name:       "all stacks failed and the database failed",
			runner:     &uwfuRunner{failBackup: true, failDB: true},
			wantStatus: "failed",
			wantMsg:    stackReason + "; " + dbReason,
		},
		{
			// Used to store a generic sentence without the database's error.
			name:       "only the database failed",
			runner:     &uwfuRunner{failDB: true},
			wantStatus: "partial",
			wantMsg:    dbReason,
		},
		{
			name:       "database failed and a requested stack is unresolved",
			runner:     &uwfuRunner{failDB: true},
			requested:  []string{uwfuStack, "no-policy"},
			wantStatus: "partial",
			wantMsg:    dbReason + "; " + unresolvedReason,
		},
		{
			// Control: a partial run with no run-level reason keeps an empty
			// message; the stack's reason lives on its item.
			name:       "one stack failed, database fine",
			runner:     &uwfuRunner{failBackup: true},
			otherStack: true,
			wantStatus: "partial",
			wantMsg:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newBackupTestDB(t)
			svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, tc.runner, tc.runner)
			seedStack(t, db, uwfuStack, "hot")
			if tc.otherStack {
				seedStack(t, db, "other-app", "hot")
			}

			run, err := svc.RunBackup(context.Background(), tc.requested, false, "manual", nil)
			require.NoError(t, err)

			stored, _ := readRun(t, db, run.ID)
			assert.Equal(t, tc.wantStatus, stored.Status)
			assert.Equal(t, tc.wantMsg, stored.ErrorMessage)
		})
	}
}

// TestRunBackup_SyncFailureKeepsEveryRunReason: the post-backup sync rewrites
// error_message after the run is final, so it must rebuild from every reason,
// not just the primary one (agent-os-bscs).
func TestRunBackup_SyncFailureKeepsEveryRunReason(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_sync_after", "true"))
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	runner := &uwfuRunner{failDB: true, failSync: true}
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)
	seedStack(t, db, uwfuStack, "hot")

	run, err := svc.RunBackup(context.Background(), []string{uwfuStack, "no-policy"}, false, "manual", nil)
	require.NoError(t, err)

	stored, _ := readRun(t, db, run.ID)
	assert.Equal(t, "partial", stored.Status)
	assert.True(t, strings.HasPrefix(stored.ErrorMessage,
		"database snapshot failed: restic backup database: database is locked; "+
			"no enabled backup policy for requested stack(s): no-policy; post-backup sync failed: "),
		"got %q", stored.ErrorMessage)
}
