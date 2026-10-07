package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration22_PausedRowsGetCompletedAt pins migration 22
// (agent-os-z91e.46). A 'paused' update_history row used to be written with no
// completed_at, and retention and the manual clear both delete by
// completed_at, so those rows could never be deleted. The backfill gives each
// one completed_at = started_at, as the scheduler now writes it. A 'pending'
// row with no completed_at is a run in flight and must stay NULL, or
// retention would delete it out from under itself; a paused row that already
// has completed_at must keep it.
func TestMigration22_PausedRowsGetCompletedAt(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 21)

	_, err = db.db.Exec(`
INSERT INTO update_history (id, container_id, container_name, image, status, trigger, started_at, completed_at) VALUES
  ('h-paused-null', 'c1', 'web', 'nginx:latest', 'paused',  'auto',   '2026-01-01T00:00:00Z', NULL),
  ('h-paused-set',  'c2', 'api', 'api:1',        'paused',  'auto',   '2026-01-02T00:00:00Z', '2026-01-02T00:05:00Z'),
  ('h-pending',     'c3', 'db',  'postgres:16',  'pending', 'manual', '2026-01-03T00:00:00Z', NULL);
`)
	require.NoError(t, err)

	require.NoError(t, RunMigrations(db))

	completedAt := func(id string) string {
		t.Helper()
		rows := dumpRows(t, db, `SELECT completed_at FROM update_history WHERE id = '`+id+`'`)
		require.Len(t, rows, 1)
		return rows[0][0]
	}
	assert.Equal(t, "2026-01-01T00:00:00Z", completedAt("h-paused-null"), "a paused row with no completed_at gets its started_at")
	assert.Equal(t, "2026-01-02T00:05:00Z", completedAt("h-paused-set"), "a paused row that has completed_at keeps it")
	assert.Equal(t, "<NULL>", completedAt("h-pending"), "a pending row is in flight and must stay incomplete")

	// Idempotent: a second run (nothing pending) changes nothing.
	require.NoError(t, RunMigrations(db))
	assert.Equal(t, "<NULL>", completedAt("h-pending"))
}
