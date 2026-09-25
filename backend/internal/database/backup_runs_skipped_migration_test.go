package database

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dumpRows returns every row of query as strings, NULL spelled "<NULL>", so two
// dumps compare column for column rather than by count.
func dumpRows(t *testing.T, db *DB, query string) [][]string {
	t.Helper()
	rows, err := db.db.Query(query)
	require.NoError(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	require.NoError(t, err)
	var out [][]string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		row := make([]string, len(cols))
		for i, v := range vals {
			if v.Valid {
				row[i] = v.String
			} else {
				row[i] = "<NULL>"
			}
		}
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestMigration19_SkippedStatusAcceptedAndDataPreserved pins migration 19
// (agent-os-4i7r). The scheduler writes status 'skipped' for a scheduled backup
// that never started, and backup_runs carries a CHECK constraint over status,
// so without the rebuild that INSERT fails at runtime. The database is built
// through v18 with the real migration SQL, seeded, and then v19 is applied by
// RunMigrations itself, which is the only way the rebuild's two FK hazards
// (see migration 12's comment) show up.
func TestMigration19_SkippedStatusAcceptedAndDataPreserved(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 18)

	// One run in every status v18 allows, each with an item, and every
	// nullable column exercised both ways.
	_, err = db.db.Exec(`
INSERT INTO backup_runs (id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message) VALUES
  ('r-running',     'backup',  'manual',    'running',     '2026-01-01T00:00:00Z', NULL,                   0, 0, 0, NULL, NULL),
  ('r-success',     'backup',  'scheduled', 'success',     '2026-01-02T00:00:00Z', '2026-01-02T00:05:00Z', 2, 2, 0, 1234, ''),
  ('r-partial',     'backup',  'scheduled', 'partial',     '2026-01-03T00:00:00Z', '2026-01-03T00:05:00Z', 2, 1, 1, 0,    'database snapshot failed: x'),
  ('r-failed',      'restore', 'manual',    'failed',      '2026-01-04T00:00:00Z', '2026-01-04T00:05:00Z', 0, 0, 0, NULL, 'restore failed'),
  ('r-interrupted', 'verify',  'manual',    'interrupted', '2026-01-05T00:00:00Z', '2026-01-05T00:05:00Z', 0, 0, 0, NULL, 'process stopped before this run completed');
INSERT INTO backup_run_items (id, run_id, stack_id, status, snapshot_id, stop_applied, duration_ms, error_message) VALUES
  ('i-running',     'r-running',     'stacks~a', 'skipped', NULL,     FALSE, NULL, NULL),
  ('i-success',     'r-success',     'stacks~a', 'success', 'abc123', TRUE,  900,  ''),
  ('i-partial',     'r-partial',     'stacks~b', 'failed',  NULL,     FALSE, 10,   'restic backup: repository locked'),
  ('i-failed',      'r-failed',      'stacks~c', 'failed',  NULL,     FALSE, NULL, 'x'),
  ('i-interrupted', 'r-interrupted', 'stacks~d', 'success', 'def456', TRUE,  5,    NULL);
`)
	require.NoError(t, err)

	const runsQ = `SELECT * FROM backup_runs ORDER BY id`
	const itemsQ = `SELECT * FROM backup_run_items ORDER BY id`
	const joinQ = `SELECT r.id, i.id FROM backup_runs r JOIN backup_run_items i ON i.run_id = r.id ORDER BY r.id`
	// Every index and trigger on the two rebuilt tables, as SQLite stores them.
	const schemaQ = `SELECT type, name, tbl_name, sql FROM sqlite_master
	                 WHERE type IN ('index','trigger') AND tbl_name IN ('backup_runs','backup_run_items') ORDER BY name`
	const tableSQLQ = `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`

	runsBefore := dumpRows(t, db, runsQ)
	itemsBefore := dumpRows(t, db, itemsQ)
	joinBefore := dumpRows(t, db, joinQ)
	schemaBefore := dumpRows(t, db, schemaQ)
	require.Len(t, runsBefore, 5)
	require.Len(t, joinBefore, 5)
	var runsSQLBefore, itemsSQLBefore string
	require.NoError(t, db.db.QueryRow(tableSQLQ, "backup_runs").Scan(&runsSQLBefore))
	require.NoError(t, db.db.QueryRow(tableSQLQ, "backup_run_items").Scan(&itemsSQLBefore))

	// Precondition: v18 refuses 'skipped'. Without this the acceptance below
	// would be consistent with a CHECK that never constrained anything.
	const insertSkipped = `INSERT INTO backup_runs (id, kind, trigger, status, started_at, finished_at, error_message)
	                       VALUES ('r-skipped', 'backup', 'scheduled', 'skipped', '2026-02-01T00:00:00Z', '2026-02-01T00:00:00Z', 'backup engine unavailable')`
	_, err = db.db.Exec(insertSkipped)
	require.Error(t, err, "'skipped' must be rejected before migration 19 widens the CHECK constraint")

	require.NoError(t, RunMigrations(db))

	var stamped int
	require.NoError(t, db.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&stamped))
	require.Equal(t, 19, stamped)

	// Every row, every column, verbatim; items still joined to their runs.
	assert.Equal(t, runsBefore, dumpRows(t, db, runsQ), "backup_runs rows must survive the rebuild unchanged")
	assert.Equal(t, itemsBefore, dumpRows(t, db, itemsQ), "backup_run_items rows must survive the rebuild unchanged")
	assert.Equal(t, joinBefore, dumpRows(t, db, joinQ), "every item must still join to its run")
	// Same indexes and triggers, same definitions.
	assert.Equal(t, schemaBefore, dumpRows(t, db, schemaQ), "every index and trigger must be recreated as it was")

	// The only schema change is the status CHECK list.
	var runsSQLAfter, itemsSQLAfter string
	require.NoError(t, db.db.QueryRow(tableSQLQ, "backup_runs").Scan(&runsSQLAfter))
	require.NoError(t, db.db.QueryRow(tableSQLQ, "backup_run_items").Scan(&itemsSQLAfter))
	const oldCheck = "CHECK (status IN ('running','success','partial','failed','interrupted'))"
	const newCheck = "CHECK (status IN ('running','success','partial','failed','interrupted','skipped'))"
	require.Equal(t, 1, strings.Count(runsSQLBefore, oldCheck))
	assert.Equal(t, strings.Replace(runsSQLBefore, oldCheck, newCheck, 1), runsSQLAfter,
		"backup_runs must differ from v18 only in its status CHECK list")
	assert.Equal(t, itemsSQLBefore, itemsSQLAfter, "backup_run_items must be rebuilt unchanged")

	assertRebuildForeignKeyIntact(t, db, "backup_run_items", "backup_runs", "backup_runs_new", "backup_run_items_v19")

	// 'skipped' is now accepted...
	_, err = db.db.Exec(insertSkipped)
	require.NoError(t, err, "'skipped' must be accepted after migration 19")
	// ...every previously-valid status still is...
	for _, status := range []string{"running", "success", "partial", "failed", "interrupted"} {
		_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES (?, 'backup', 'manual', ?, '2026-02-01T00:00:00Z')`,
			"r-new-"+status, status)
		assert.NoError(t, err, "status %q must still be accepted", status)
	}
	// ...and the CHECK still exists: an unknown status is refused.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('r-bogus', 'backup', 'manual', 'bogus', '2026-02-01T00:00:00Z')`)
	require.Error(t, err, "an unknown status must still be rejected after the rebuild")

	// The FK still resolves, and still refuses an orphan.
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status) VALUES ('i-new', 'r-skipped', 'stacks~e', 'success')`)
	require.NoError(t, err, "backup_run_items' FK must still resolve after the rebuild")
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status) VALUES ('i-orphan', 'no-such-run', 'stacks~f', 'success')`)
	require.Error(t, err, "the FK must still reject a run_id with no parent row")
}
