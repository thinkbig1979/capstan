package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration16_VerifyKindAcceptedAndDataPreserved pins migration 16
// (agent-os-j1jw). services.RunKindVerify writes 'verify' into
// backup_runs.kind, and backup_runs carries a CHECK constraint over that
// column, so without the rebuild every LaunchVerify INSERT fails at runtime --
// a failure that no compiler and no argv test can see.
//
// It mirrors TestMigration_BackupRunsInterruptedStatus_PreservesDataAndFK: the
// same rebuild recipe carries the same two hazards (SQLite rewriting
// backup_run_items' FK text on a RENAME, and the FK CASCADE firing during
// DROP TABLE inside the migration runner's transaction), and both only show up
// when migrations are run for real rather than when the resulting schema is
// inspected.
func TestMigration16_VerifyKindAcceptedAndDataPreserved(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Build the pre-v16 schema shape (mirrors migration v12's output).
	_, err = db.db.Exec(`
CREATE TABLE backup_runs (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN ('backup','sync','restore','dr_restore','prune')),
    trigger       TEXT NOT NULL CHECK (trigger IN ('manual','scheduled')),
    status        TEXT NOT NULL CHECK (status IN ('running','success','partial','failed','interrupted')),
    started_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at   DATETIME,
    stacks_total  INTEGER NOT NULL DEFAULT 0,
    stacks_ok     INTEGER NOT NULL DEFAULT 0,
    stacks_failed INTEGER NOT NULL DEFAULT 0,
    bytes_added   INTEGER,
    error_message TEXT
);
CREATE TABLE backup_run_items (
    id            TEXT PRIMARY KEY,
    run_id        TEXT NOT NULL,
    stack_id      TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('skipped','success','failed')),
    snapshot_id   TEXT,
    stop_applied  BOOLEAN NOT NULL DEFAULT FALSE,
    duration_ms   INTEGER,
    error_message TEXT,
    FOREIGN KEY (run_id) REFERENCES backup_runs(id) ON DELETE CASCADE
);
`)
	require.NoError(t, err)

	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed)
	                      VALUES ('run-legacy', 'backup', 'manual', 'success', '2026-01-01T00:00:00Z', '2026-01-01T00:05:00Z', 2, 2, 0)`)
	require.NoError(t, err)
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status, snapshot_id)
	                      VALUES ('item-legacy', 'run-legacy', 'stacks~a', 'success', 'abc123')`)
	require.NoError(t, err)

	// Precondition. Without this the post-migration success below would be
	// consistent with a CHECK constraint that never constrained anything.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('run-pre', 'verify', 'manual', 'running', '2026-01-01T00:00:00Z')`)
	require.Error(t, err, "'verify' must be rejected before migration 16 widens the CHECK constraint")

	require.NoError(t, RunMigrations(db))

	// The kind the new run type writes is now accepted.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('run-verify', 'verify', 'manual', 'running', '2026-02-01T00:00:00Z')`)
	require.NoError(t, err, "'verify' must be accepted after migration 16")

	// Every previously-valid kind still is: this widens the set, it does not
	// replace it.
	for _, kind := range []string{"backup", "sync", "restore", "dr_restore", "prune"} {
		_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES (?, ?, 'manual', 'running', '2026-02-01T00:00:00Z')`, "run-"+kind, kind)
		assert.NoError(t, err, "kind %q must still be accepted", kind)
	}

	// A kind outside the set is still refused, so the rebuild did not simply
	// drop the constraint.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('run-bogus', 'bogus', 'manual', 'running', '2026-02-01T00:00:00Z')`)
	require.Error(t, err, "an unknown kind must still be rejected after the rebuild")

	// The legacy parent row survived the rebuild verbatim.
	var status, startedAt string
	require.NoError(t, db.db.QueryRow(`SELECT status, started_at FROM backup_runs WHERE id = 'run-legacy'`).Scan(&status, &startedAt))
	assert.Equal(t, "success", status)
	assert.Equal(t, "2026-01-01T00:00:00Z", startedAt)

	// The child row survived too -- this is the DROP-CASCADE hazard migration
	// 12's comment documents, and it is live for this rebuild as well.
	var itemStack string
	require.NoError(t, db.db.QueryRow(`SELECT stack_id FROM backup_run_items WHERE id = 'item-legacy'`).Scan(&itemStack))
	assert.Equal(t, "stacks~a", itemStack)

	// The FK is still wired to a table that exists, so later inserts work.
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status) VALUES ('item-new', 'run-verify', 'stacks~b', 'success')`)
	require.NoError(t, err, "backup_run_items' FK must still resolve after the rebuild")

	// And it still refuses an orphan.
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status) VALUES ('item-orphan', 'no-such-run', 'stacks~c', 'success')`)
	require.Error(t, err, "the FK must still reject a run_id with no parent row")
}
