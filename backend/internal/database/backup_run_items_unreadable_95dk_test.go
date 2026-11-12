package database

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// --- agent-os-95dk / agent-os-fmeu: backup_run_items records whether an
// item's snapshot is partial (migration 26). ---

// Migration 26 backfills the count from the item note, but only from the two
// exact sentences unreadableNote writes. Every other row stays NULL, which
// reads as a complete snapshot.
func TestMigration26_95dk_BackfillsOnlyTheExactSufnNotes(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	applyMigrationsThrough(t, db, 25)

	_, err = db.db.Exec(`
INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('r1', 'backup', 'manual', 'partial', '2026-11-01T00:00:00Z');
INSERT INTO backup_run_items (id, run_id, stack_id, status, snapshot_id, stop_applied, duration_ms, error_message) VALUES
  ('a-alone',     'r1', 's', 'success', 'aaaaaaaa', 0, 1, 'snapshot saved without 2 unreadable path(s): "/x/a", "/x/b"'),
  ('b-after',     'r1', 's', 'success', 'bbbbbbbb', 0, 1, 'verify warning: x; snapshot saved without 500 unreadable path(s): "/x/a" (and 480 more, not listed)'),
  ('c-none',      'r1', 's', 'success', 'cccccccc', 0, 1, 'snapshot saved, but restic could not read some files and named none'),
  ('d-none-mid',  'r1', 's', 'success', 'dddddddd', 0, 1, 'a; snapshot saved, but restic could not read some files and named none; b'),
  ('e-complete',  'r1', 's', 'success', 'eeeeeeee', 0, 1, ''),
  ('f-null',      'r1', 's', 'success', 'ffffffff', 0, 1, NULL),
  ('g-midword',   'r1', 's', 'success', 'gggggggg', 0, 1, 'xsnapshot saved without 3 unreadable path(s): "/x/a"'),
  ('h-notdigits', 'r1', 's', 'success', 'hhhhhhhh', 0, 1, 'snapshot saved without many unreadable path(s): "/x/a"'),
  ('i-failed',    'r1', 's', 'failed',  NULL,       0, 1, 'snapshot saved without 4 unreadable path(s): "/x/a"'),
  ('j-prefix',    'r1', 's', 'success', 'jjjjjjjj', 0, 1, 'snapshot saved, but restic could not read some files and named none at all');
`)
	require.NoError(t, err)
	require.NoError(t, RunMigrations(db))

	want := map[string]sql.NullInt64{
		"a-alone":     {Int64: 2, Valid: true},
		"b-after":     {Int64: 500, Valid: true},
		"c-none":      {Int64: 0, Valid: true},
		"d-none-mid":  {Int64: 0, Valid: true},
		"e-complete":  {},
		"f-null":      {},
		"g-midword":   {},
		"h-notdigits": {},
		"i-failed":    {},
		"j-prefix":    {},
	}
	rows, err := db.db.Query(`SELECT id, unreadable_count, unreadable_paths FROM backup_run_items`)
	require.NoError(t, err)
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id string
		var count sql.NullInt64
		var paths sql.NullString
		require.NoError(t, rows.Scan(&id, &count, &paths))
		assert.Equal(t, want[id], count, "item %s", id)
		assert.False(t, paths.Valid, "item %s: paths are never backfilled from the note", id)
		seen++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, len(want), seen)
}

// The two columns round-trip through the item writer and readers.
func TestBackupRunItem_95dk_UnreadableColumnsRoundTrip(t *testing.T) {
	db := newTestDB(t)
	run := &models.BackupRun{ID: "r1", Kind: "backup", Trigger: "manual", Status: "partial", StartedAt: "2026-11-01T00:00:00Z"}
	require.NoError(t, db.CreateBackupRun(run))
	three := 3
	require.NoError(t, db.AddBackupRunItem(&models.BackupRunItem{ID: "partial", RunID: "r1", StackID: "s", Status: "success",
		SnapshotID: "aaaaaaaa", UnreadableCount: &three, UnreadablePaths: []string{"/x/a", "/x/b, c"}}))
	require.NoError(t, db.AddBackupRunItem(&models.BackupRunItem{ID: "complete", RunID: "r1", StackID: "t", Status: "success", SnapshotID: "bbbbbbbb"}))

	items, err := db.GetBackupRunItems("r1")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.NotNil(t, items[1].UnreadableCount)
	assert.Equal(t, 3, *items[1].UnreadableCount)
	assert.Equal(t, []string{"/x/a", "/x/b, c"}, items[1].UnreadablePaths)
	assert.Nil(t, items[0].UnreadableCount)
	assert.Empty(t, items[0].UnreadablePaths)

	item, err := db.GetRunItemForSnapshot("s", "aaaaaaaa0000000000")
	require.NoError(t, err, "a full id matches the stored short id")
	assert.Equal(t, "partial", item.ID)
	_, err = db.GetRunItemForSnapshot("s", "cccccccc")
	assert.True(t, errors.Is(err, errdefs.ErrNotFound), "%v", err)
}

// The newest complete snapshot skips newer partial items and failed ones.
func TestGetNewestCompleteSnapshotID_95dk(t *testing.T) {
	db := newTestDB(t)
	one := 1
	add := func(run, started, item, status, snap string, count *int) {
		t.Helper()
		require.NoError(t, db.CreateBackupRun(&models.BackupRun{ID: run, Kind: "backup", Trigger: "manual", Status: "success", StartedAt: started}))
		require.NoError(t, db.AddBackupRunItem(&models.BackupRunItem{ID: item, RunID: run, StackID: "s", Status: status, SnapshotID: snap, UnreadableCount: count}))
	}
	_, err := db.GetNewestCompleteSnapshotID("s")
	assert.True(t, errors.Is(err, errdefs.ErrNotFound), "%v", err)

	add("r1", "2026-11-01T00:00:00Z", "i1", "success", "11111111", nil)
	add("r2", "2026-11-02T00:00:00Z", "i2", "success", "22222222", nil)
	add("r3", "2026-11-03T00:00:00Z", "i3", "success", "33333333", &one)
	add("r4", "2026-11-04T00:00:00Z", "i4", "failed", "", nil)

	id, err := db.GetNewestCompleteSnapshotID("s")
	require.NoError(t, err)
	assert.Equal(t, "22222222", id)
}
