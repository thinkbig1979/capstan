package services

import (
	"context"
	"errors"
	"strings"
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
