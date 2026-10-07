package database

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration21_UpdateHistorySkippedAcceptedAndDataPreserved pins migration
// 21 (agent-os-z91e.32). RunAutoUpdates writes status 'skipped' for an item a
// pass never started or skipped because its stack was busy, and update_history
// carries a CHECK constraint over status, so without the rebuild that INSERT
// fails at runtime. The database is built through v20 with the real migration
// SQL, seeded, and v21 is then applied by RunMigrations itself.
func TestMigration21_UpdateHistorySkippedAcceptedAndDataPreserved(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 20)

	// One row in every status v20 allows, every nullable column exercised
	// both ways.
	_, err = db.db.Exec(`
INSERT INTO update_history (id, container_id, container_name, stack_id, stack_name, image, old_digest, new_digest, old_image_ref, new_image_ref, status, trigger, started_at, completed_at, duration_ms, error_message) VALUES
  ('h-pending', 'c1', 'web', 'stacks~a', 'a',  'nginx:latest', NULL,        NULL,        NULL,    NULL,    'pending', 'manual', '2026-01-01T00:00:00Z', NULL,                   NULL, NULL),
  ('h-success', 'c2', 'api', 'stacks~b', 'b',  'api:1',        'sha256:aa', 'sha256:bb', 'api:1', 'api:1', 'success', 'auto',   '2026-01-02T00:00:00Z', '2026-01-02T00:01:00Z', 1500, NULL),
  ('h-failed',  'c3', 'db',  NULL,       NULL, 'postgres:16',  'sha256:cc', NULL,        NULL,    NULL,    'failed',  'auto',   '2026-01-03T00:00:00Z', '2026-01-03T00:02:00Z', 20,   'docker pull failed'),
  ('h-paused',  'c3', 'db',  NULL,       NULL, 'postgres:16',  NULL,        NULL,        NULL,    NULL,    'paused',  'auto',   '2026-01-03T00:02:00Z', NULL,                   NULL, '');
`)
	require.NoError(t, err)

	const rowsQ = `SELECT * FROM update_history ORDER BY id`
	// Every index and trigger on the rebuilt table, as SQLite stores them.
	const schemaQ = `SELECT type, name, tbl_name, sql FROM sqlite_master
	                 WHERE type IN ('index','trigger') AND tbl_name = 'update_history' ORDER BY name`
	const tableSQLQ = `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'update_history'`

	rowsBefore := dumpRows(t, db, rowsQ)
	schemaBefore := dumpRows(t, db, schemaQ)
	require.Len(t, rowsBefore, 4)
	// The five from migration 3 plus idx_update_history_completed_at from
	// migration 11; the autoindex for the TEXT PRIMARY KEY has a NULL sql.
	names := make([]string, 0, len(schemaBefore))
	for _, r := range schemaBefore {
		names = append(names, r[1])
	}
	require.Subset(t, names, []string{
		"idx_update_history_completed_at", "idx_update_history_container_id", "idx_update_history_stack_id",
		"idx_update_history_started_at", "idx_update_history_status", "idx_update_history_trigger",
	})
	var tableSQLBefore string
	require.NoError(t, db.db.QueryRow(tableSQLQ).Scan(&tableSQLBefore))

	// Precondition: v20 refuses 'skipped'. Without this the acceptance below
	// would be consistent with a CHECK that never constrained anything.
	const insertSkipped = `INSERT INTO update_history (id, container_id, container_name, image, status, trigger, started_at, completed_at, error_message)
	                       VALUES ('h-skipped', 'c4', 'cache', 'redis:7', 'skipped', 'auto', '2026-02-01T00:00:00Z', '2026-02-01T00:00:00Z', 'not started: pass deadline reached; retried next pass')`
	_, err = db.db.Exec(insertSkipped)
	require.Error(t, err, "'skipped' must be rejected before migration 21 widens the CHECK constraint")

	require.NoError(t, RunMigrations(db))

	var stamped int
	require.NoError(t, db.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&stamped))
	require.Equal(t, migrations[len(migrations)-1].Version, stamped)

	// RunMigrations also applies migration 22, which backfills completed_at
	// on h-paused (agent-os-z91e.46); every other value must be unchanged.
	// Columns 12 and 13 of SELECT * are started_at and completed_at.
	for _, r := range rowsBefore {
		if r[0] == "h-paused" {
			require.Equal(t, "<NULL>", r[13])
			r[13] = r[12]
		}
	}
	assert.Equal(t, rowsBefore, dumpRows(t, db, rowsQ), "update_history rows must survive the rebuild unchanged")
	assert.Equal(t, schemaBefore, dumpRows(t, db, schemaQ), "every index and trigger must be recreated as it was")

	// The only schema change is the status CHECK list.
	var tableSQLAfter string
	require.NoError(t, db.db.QueryRow(tableSQLQ).Scan(&tableSQLAfter))
	const oldCheck = "CHECK (status IN ('pending', 'success', 'failed', 'paused'))"
	const newCheck = "CHECK (status IN ('pending', 'success', 'failed', 'paused', 'skipped'))"
	require.Equal(t, 1, strings.Count(tableSQLBefore, oldCheck))
	assert.Equal(t, normalizeTableSQL(strings.Replace(tableSQLBefore, oldCheck, newCheck, 1)), normalizeTableSQL(tableSQLAfter),
		"update_history must differ from v20 only in its status CHECK list")

	// 'skipped' is now accepted...
	_, err = db.db.Exec(insertSkipped)
	require.NoError(t, err, "'skipped' must be accepted after migration 21")
	// ...every previously-valid status still is...
	for _, status := range []string{"pending", "success", "failed", "paused"} {
		_, err = db.db.Exec(`INSERT INTO update_history (id, container_id, container_name, image, status, trigger) VALUES (?, 'c9', 'x', 'x:1', ?, 'auto')`,
			"h-new-"+status, status)
		assert.NoError(t, err, "status %q must still be accepted", status)
	}
	// ...and the CHECKs still exist: an unknown status or trigger is refused.
	_, err = db.db.Exec(`INSERT INTO update_history (id, container_id, container_name, image, status, trigger) VALUES ('h-bogus', 'c9', 'x', 'x:1', 'bogus', 'auto')`)
	require.Error(t, err, "an unknown status must still be rejected after the rebuild")
	_, err = db.db.Exec(`INSERT INTO update_history (id, container_id, container_name, image, status, trigger) VALUES ('h-bogus-trigger', 'c9', 'x', 'x:1', 'skipped', 'cron')`)
	require.Error(t, err, "the trigger CHECK must survive the rebuild")
}

// normalizeTableSQL collapses whitespace runs and drops the name quoting
// SQLite adds when a table is renamed, so two CREATE TABLE texts compare by
// content rather than by layout.
func normalizeTableSQL(s string) string {
	s = strings.ReplaceAll(s, `"update_history"`, "update_history")
	s = strings.Replace(s, "CREATE TABLE IF NOT EXISTS ", "CREATE TABLE ", 1)
	return strings.Join(strings.Fields(s), " ")
}
