package database

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// canonicalCompletedAt is the one spelling update_history's timestamps are
// stored in: UTC, whole seconds, 'Z'. Retention and the manual clear compare
// completed_at as text against that spelling.
var canonicalCompletedAt = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

func historyByID(t *testing.T, db *DB) map[string]models.UpdateHistoryEntry {
	t.Helper()
	rows, _, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Limit: 100})
	require.NoError(t, err)
	byID := make(map[string]models.UpdateHistoryEntry, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	return byID
}

// An update cut off by a restart leaves a 'pending' row that never gets a
// completed_at, and retention and the manual clear both delete by completed_at
// (agent-os-2zv2). It goes through a real reopen, as main.go does, so only the
// startup path can bring the row to a terminal state.
func TestOpenDB_SweepsPendingUpdateHistoryToFailed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seed, err := NewWithMigrations(dir)
	require.NoError(t, err)

	done := "2026-07-31T17:55:00Z"
	for _, e := range []*models.UpdateHistoryEntry{
		{ID: "h-pending", ContainerID: "c1", ContainerName: "web", Image: "nginx", Status: "pending", Trigger: "manual", StartedAt: "2026-07-31T17:50:00Z"},
		{ID: "h-success", ContainerID: "c2", ContainerName: "db", Image: "pg", Status: "success", Trigger: "auto", StartedAt: "2026-07-31T17:50:00Z", CompletedAt: &done},
		{ID: "h-paused", ContainerID: "c3", ContainerName: "q", Image: "redis", Status: "paused", Trigger: "auto", StartedAt: "2026-07-31T17:50:00Z", CompletedAt: &done},
	} {
		require.NoError(t, seed.InsertUpdateHistory(e))
	}
	require.NoError(t, seed.Close())

	before := time.Now().UTC().Add(-2 * time.Second)
	restarted, err := NewWithMigrations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { restarted.Close() })

	got := historyByID(t, restarted)

	stuck := got["h-pending"]
	assert.Equal(t, "failed", stuck.Status, "a row still 'pending' at startup must reach a terminal state")
	require.NotNil(t, stuck.CompletedAt, "completed_at must be set, or retention can never delete the row")
	assert.Regexp(t, canonicalCompletedAt, *stuck.CompletedAt, "completed_at must use the column's canonical spelling")
	completed, perr := time.Parse(time.RFC3339, *stuck.CompletedAt)
	require.NoError(t, perr)
	assert.False(t, completed.Before(before), "completed_at is the sweep time, not the stale started_at")
	require.NotNil(t, stuck.ErrorMessage)
	assert.Equal(t, "interrupted by a restart", *stuck.ErrorMessage)

	assert.Equal(t, "success", got["h-success"].Status)
	assert.Equal(t, done, *got["h-success"].CompletedAt)
	assert.Nil(t, got["h-success"].ErrorMessage, "a finished row is not touched")
	assert.Equal(t, "paused", got["h-paused"].Status)
	assert.Equal(t, done, *got["h-paused"].CompletedAt)
}

// A second startup finds nothing in flight, so it must leave the swept row's
// first completed_at alone.
func TestOpenDB_UpdateHistorySweepIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seed, err := NewWithMigrations(dir)
	require.NoError(t, err)
	require.NoError(t, seed.InsertUpdateHistory(&models.UpdateHistoryEntry{
		ID: "h-pending", ContainerID: "c1", ContainerName: "web", Image: "nginx",
		Status: "pending", Trigger: "manual", StartedAt: "2026-07-31T17:50:00Z",
	}))
	require.NoError(t, seed.Close())

	first, err := NewWithMigrations(dir)
	require.NoError(t, err)
	swept := historyByID(t, first)["h-pending"]
	require.Equal(t, "failed", swept.Status)
	require.NotNil(t, swept.CompletedAt)
	require.NoError(t, first.Close())

	second, err := NewWithMigrations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { second.Close() })
	again := historyByID(t, second)["h-pending"]
	assert.Equal(t, "failed", again.Status)
	require.NotNil(t, again.CompletedAt)
	assert.Equal(t, *swept.CompletedAt, *again.CompletedAt)
}
