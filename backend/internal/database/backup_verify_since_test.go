package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestHasFinishedVerifySince pins which rows count as "checked this week" for
// the scheduled repository check (agent-os-ffaj): verify runs that ended in
// success or failed, started at or after since. Each case stores ONE row, so
// the answer is about that row alone.
func TestHasFinishedVerifySince(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	// A zone other than UTC on the bound: it must be compared as the instant,
	// not as its spelling.
	amsterdam := time.FixedZone("CEST", 2*60*60)

	tests := []struct {
		name      string
		kind      string
		status    string
		startedAt time.Time
		want      bool
	}{
		{"success after since", "verify", "success", since.Add(time.Hour), true},
		{"failed after since", "verify", "failed", since.Add(time.Hour), true},
		{"exactly at since counts", "verify", "success", since, true},
		{"one second before since does not", "verify", "success", since.Add(-time.Second), false},
		{"interrupted does not count", "verify", "interrupted", since.Add(time.Hour), false},
		{"skipped does not count", "verify", "skipped", since.Add(time.Hour), false},
		{"running does not count", "verify", "running", since.Add(time.Hour), false},
		{"a backup run is not a check", "backup", "success", since.Add(time.Hour), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			require.NoError(t, db.CreateBackupRun(&models.BackupRun{
				ID:        "r1",
				Kind:      tc.kind,
				Trigger:   "scheduled",
				Status:    tc.status,
				StartedAt: tc.startedAt.UTC().Format(time.RFC3339),
			}))

			got, err := db.HasFinishedVerifySince(since)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			gotZoned, err := db.HasFinishedVerifySince(since.In(amsterdam))
			require.NoError(t, err)
			assert.Equal(t, tc.want, gotZoned, "same instant in another zone")
		})
	}
}
