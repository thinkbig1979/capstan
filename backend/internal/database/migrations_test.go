package database

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// assertRebuildForeignKeyIntact is the reusable regression assertion for any
// migration that rebuilds a table with an incoming foreign key (see the
// "Table rebuilds in SQLite" comment at the top of migrations.go).
// foreign_key_check alone is not sufficient -- a dangling reference can pass
// depending on how SQLite resolves it at check time -- so this also reads
// childTable's FK text straight out of sqlite_master and asserts it still
// names parentTable, which is what catches a rebuild that reintroduces
// migration 9's rename-out pattern and leaves the FK pointing at a
// transient rename-target name (e.g. "<parent>_old", "<parent>_new") that no
// longer exists once the migration finishes. Must be called after the
// migration under test has actually run through RunMigrations -- the FK
// text is only ever wrong to begin with under real transactional execution
// (see the same file-header comment), so a hand-assembled probe would not
// exercise the hazard this guards against.
func assertRebuildForeignKeyIntact(t *testing.T, db *DB, childTable, parentTable string, forbiddenNames ...string) {
	t.Helper()

	var childSchemaSQL string
	require.NoError(t, db.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, childTable,
	).Scan(&childSchemaSQL))
	assert.Contains(t, childSchemaSQL, fmt.Sprintf("REFERENCES %s(", parentTable),
		fmt.Sprintf("%s's FK must reference the final %q table by name, not a transient rebuild name", childTable, parentTable))
	for _, forbidden := range forbiddenNames {
		assert.NotContains(t, childSchemaSQL, forbidden,
			fmt.Sprintf("the FK must not be left pointing at the transient rebuild name %q", forbidden))
	}

	fkRows, err := db.db.Query(`PRAGMA foreign_key_check`)
	require.NoError(t, err)
	var fkViolations int
	for fkRows.Next() {
		fkViolations++
	}
	require.NoError(t, fkRows.Close())
	assert.Equal(t, 0, fkViolations, "the rebuild must leave zero foreign_key_check violations")
}

// TestMigration_ActionLogDenormalized_PreservesDataVerbatim exercises the
// real upgrade path for migration v9: it builds the pre-v9 action_log shape
// (user_id TEXT NOT NULL with an FK to users(id), stack_id TEXT with an FK to
// stacks(id)), inserts a legacy row whose user_id ("anonymous") has no
// matching users row -- exactly what a database created before this fix
// would contain, since the old pool-wide FK enforcement bug (agent-os-94t)
// let such rows slip in on most connections -- then runs the full migration
// set and asserts the row survives with its user_id preserved verbatim
// (agent-os-z4v's redirected design: action_log is a denormalized,
// append-only audit record with no FKs, so sentinel actor labels like
// "anonymous"/"system" are legitimate values, not placeholders to be nulled).
func TestMigration_ActionLogDenormalized_PreservesDataVerbatim(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Build the exact pre-v9 schema shape (mirrors migration v1) so the
	// migration path rebuilds *this* table rather than a fresh one.
	_, err = db.db.Exec(`
CREATE TABLE users (
	id TEXT PRIMARY KEY,
	username TEXT UNIQUE NOT NULL,
	password TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE stacks (
	id TEXT PRIMARY KEY,
	directory TEXT NOT NULL,
	compose_file TEXT NOT NULL,
	env_file TEXT,
	project_name TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'unknown',
	is_git_repo BOOLEAN NOT NULL DEFAULT 0,
	git_branch TEXT,
	git_commit TEXT,
	git_dirty BOOLEAN NOT NULL DEFAULT 0,
	git_ahead INTEGER NOT NULL DEFAULT 0,
	git_behind INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE action_log (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	stack_id TEXT,
	action TEXT NOT NULL,
	detail TEXT,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE
);
`)
	require.NoError(t, err)

	// Seed a real user (should survive the migration unchanged) and a
	// legacy orphaned row shaped like data the FK-enforcement bug allowed
	// through in production. The orphan row could only have been written in
	// the first place because foreign_keys enforcement wasn't actually
	// active on whatever connection wrote it (agent-os-94t); reproduce that
	// by disabling enforcement for the seed step only.
	_, err = db.db.Exec(`INSERT INTO users (id, username, password) VALUES ('u-1', 'admin', 'hash')`)
	require.NoError(t, err)
	_, err = db.db.Exec(`PRAGMA foreign_keys=OFF`)
	require.NoError(t, err)
	_, err = db.db.Exec(`INSERT INTO action_log (id, user_id, stack_id, action, detail, created_at)
	                      VALUES ('al-legacy-orphan', 'anonymous', NULL, 'login', '{}', '2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	_, err = db.db.Exec(`INSERT INTO action_log (id, user_id, stack_id, action, detail, created_at)
	                      VALUES ('al-legacy-real', 'u-1', NULL, 'login', '{}', '2026-01-02T00:00:00Z')`)
	require.NoError(t, err)
	_, err = db.db.Exec(`PRAGMA foreign_keys=ON`)
	require.NoError(t, err)

	// Now run the full migration set (1 through the latest, including v9).
	require.NoError(t, RunMigrations(db))

	var orphanUserID string
	require.NoError(t, db.db.QueryRow(`SELECT user_id FROM action_log WHERE id = 'al-legacy-orphan'`).Scan(&orphanUserID))
	assert.Equal(t, "anonymous", orphanUserID, "a legacy actor label must be preserved verbatim, not nulled or rewritten")

	var realUserID string
	require.NoError(t, db.db.QueryRow(`SELECT user_id FROM action_log WHERE id = 'al-legacy-real'`).Scan(&realUserID))
	assert.Equal(t, "u-1", realUserID, "a real user's action_log row must keep its user_id across the migration")

	// The API-facing read path must return the same values that were stored.
	entries, _, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{})
	require.NoError(t, err)
	byID := map[string]models.ActionLog{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	require.Contains(t, byID, "al-legacy-orphan")
	assert.Equal(t, "anonymous", byID["al-legacy-orphan"].UserID)
	require.Contains(t, byID, "al-legacy-real")
	assert.Equal(t, "u-1", byID["al-legacy-real"].UserID)

	// Going forward, LogAction must persist any actor label verbatim, with no
	// FK to validate against, so a placeholder like "system" (used for
	// background jobs) succeeds without erroring.
	require.NoError(t, db.LogAction(models.ActionLog{
		ID:      "al-fresh-system",
		UserID:  "system",
		Action:  "backup",
		Detail:  "{}",
		StackID: "",
	}))
	var freshUserID string
	require.NoError(t, db.db.QueryRow(`SELECT user_id FROM action_log WHERE id = 'al-fresh-system'`).Scan(&freshUserID))
	assert.Equal(t, "system", freshUserID, "LogAction must store an unrecognized actor label verbatim, not fail or null it")
}

// TestMigration_ActionLogDenormalized_IndexesRecreated confirms the v9 table
// rebuild recreates the indexes migration v1 originally created, so query
// plans for action_log lookups aren't silently degraded.
func TestMigration_ActionLogDenormalized_IndexesRecreated(t *testing.T) {
	db := newTestDB(t)

	rows, err := db.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'action_log'`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	assert.Contains(t, names, "idx_action_log_user_id")
	assert.Contains(t, names, "idx_action_log_stack_id")
	assert.Contains(t, names, "idx_action_log_created_at")
}

// TestActionLog_SurvivesUserDeletion is the cascade-erasure fix this redirect
// exists for: before migration v9, action_log.user_id had an ON DELETE
// CASCADE FK to users(id), so deleting a user erased their entire audit
// trail. With foreign_keys enforcement genuinely active pool-wide
// (agent-os-94t) that flaw would have started firing in production; v9
// removes the FK so the audit record outlives the actor it describes.
func TestActionLog_SurvivesUserDeletion(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.CreateUser(models.User{
		ID: "u-doomed", Username: "doomed", Password: "hash",
	}))
	require.NoError(t, db.LogAction(models.ActionLog{
		ID: "al-1", UserID: "u-doomed", Action: "login", Detail: "{}",
	}))

	_, err := db.db.Exec(`DELETE FROM users WHERE id = ?`, "u-doomed")
	require.NoError(t, err, "deleting the user must not fail or cascade-error")

	var userID string
	err = db.db.QueryRow(`SELECT user_id FROM action_log WHERE id = 'al-1'`).Scan(&userID)
	require.NoError(t, err, "the action_log row must still exist after its actor's user row is deleted")
	assert.Equal(t, "u-doomed", userID, "the original actor label must be preserved even though the user no longer exists")
}

// TestActionLog_SurvivesStackDeletion is the stack-side twin of the same
// cascade-erasure flaw: action_log.stack_id also had an ON DELETE CASCADE FK
// to stacks(id) pre-v9, so deleting a stack erased its audit history.
func TestActionLog_SurvivesStackDeletion(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/opt/stacks/doomed", Name: "doomed"}))
	stackID := "stacks~doomed"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: stackID, Directory: "/opt/stacks/doomed", ComposeFile: "compose.yaml", ProjectName: "doomed",
	}))
	require.NoError(t, db.LogAction(models.ActionLog{
		ID: "al-2", UserID: "admin", StackID: stackID, Action: "delete", Detail: "{}",
	}))

	require.NoError(t, db.DeleteStack(stackID))

	var gotStackID sql.NullString
	err := db.db.QueryRow(`SELECT stack_id FROM action_log WHERE id = 'al-2'`).Scan(&gotStackID)
	require.NoError(t, err, "the action_log row must still exist after its stack is deleted")
	require.True(t, gotStackID.Valid)
	assert.Equal(t, stackID, gotStackID.String, "the original stack_id must be preserved even though the stack no longer exists")
}

// TestActionLog_AnonymousAndSystemLabels_PersistWithFKEnforcementOn proves
// the actual fix under real pool-wide FK enforcement (not just against a
// schema with no FKs by inspection): sentinel actor labels must insert and
// read back successfully on a DB built via the production constructor.
func TestActionLog_AnonymousAndSystemLabels_PersistWithFKEnforcementOn(t *testing.T) {
	db := newTestDB(t)

	var fkOn int
	require.NoError(t, db.db.QueryRow("PRAGMA foreign_keys").Scan(&fkOn))
	require.Equal(t, 1, fkOn, "test precondition: foreign_keys must be ON")

	require.NoError(t, db.LogAction(models.ActionLog{ID: "al-anon", UserID: "anonymous", Action: "login", Detail: "{}"}))
	require.NoError(t, db.LogAction(models.ActionLog{ID: "al-sys", UserID: "system", Action: "backup", Detail: "{}"}))

	entries, _, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{})
	require.NoError(t, err)
	byID := map[string]models.ActionLog{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	require.Contains(t, byID, "al-anon")
	assert.Equal(t, "anonymous", byID["al-anon"].UserID)
	require.Contains(t, byID, "al-sys")
	assert.Equal(t, "system", byID["al-sys"].UserID)
}

// TestMigration_BackupRunsInterruptedStatus_PreservesDataAndFK exercises the
// real upgrade path for migration v12 (agent-os-pid): it builds the pre-v12
// backup_runs/backup_run_items shape (mirrors migration v8, CHECK constraint
// without 'interrupted'), seeds a parent row with a child item row -- exactly
// what a real database contains going into the upgrade -- then runs the full
// migration set and asserts:
//   - the seeded rows survive the rebuild verbatim
//   - the child item's FK to the rebuilt parent is still live (insert +
//     cascade delete both still work), which is the exact failure mode ruled
//     out empirically before this migration was written: a naive
//     rename-old/create-new-under-the-old-name/drop-old rebuild (the pattern
//     migration v9 uses for action_log, which has no incoming FK) leaves
//     backup_run_items' FK pointing at a table name that no longer exists
//   - 'interrupted' is rejected before the migration and accepted after
func TestMigration_BackupRunsInterruptedStatus_PreservesDataAndFK(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Build the exact pre-v12 schema shape (mirrors migration v8).
	_, err = db.db.Exec(`
CREATE TABLE backup_runs (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN ('backup','sync','restore','dr_restore','prune')),
    trigger       TEXT NOT NULL CHECK (trigger IN ('manual','scheduled')),
    status        TEXT NOT NULL CHECK (status IN ('running','success','partial','failed')),
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

	// Seed a pre-migration parent row and a child item row.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed)
	                      VALUES ('run-legacy', 'backup', 'manual', 'success', '2026-01-01T00:00:00Z', '2026-01-01T00:05:00Z', 2, 2, 0)`)
	require.NoError(t, err)
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status, snapshot_id)
	                      VALUES ('item-legacy', 'run-legacy', 'stacks~a', 'success', 'abc123')`)
	require.NoError(t, err)

	// Precondition: 'interrupted' must be rejected by the pre-migration CHECK.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('run-pre', 'backup', 'manual', 'interrupted', '2026-01-01T00:00:00Z')`)
	require.Error(t, err, "'interrupted' must be rejected before the migration widens the CHECK constraint")

	// Run the full migration set (1 through the latest, including v12).
	require.NoError(t, RunMigrations(db))

	// The legacy parent row must survive the rebuild verbatim.
	var status string
	var finishedAt sql.NullString
	require.NoError(t, db.db.QueryRow(`SELECT status, finished_at FROM backup_runs WHERE id = 'run-legacy'`).Scan(&status, &finishedAt))
	assert.Equal(t, "success", status)
	require.True(t, finishedAt.Valid)
	assert.Equal(t, "2026-01-01T00:05:00Z", finishedAt.String)

	// The legacy child row must still be there, still linked to its parent.
	var itemCount int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM backup_run_items WHERE id = 'item-legacy' AND run_id = 'run-legacy'`).Scan(&itemCount))
	assert.Equal(t, 1, itemCount, "the child item row must survive the parent table rebuild")

	// Belt and braces on the FK, per review: validity (foreign_key_check) is
	// not sufficient on its own -- a dangling reference can pass depending on
	// how SQLite resolves it at check time. Assert the FK's stored TEXT too,
	// so a future migration that reintroduces migration 9's rename-out
	// pattern (leaving this pointing at "backup_runs_v11" or
	// "backup_runs_new") is caught even if foreign_key_check somehow does not
	// flag it.
	assertRebuildForeignKeyIntact(t, db, "backup_run_items", "backup_runs", "backup_runs_v11", "backup_runs_new")

	// ON DELETE CASCADE must still fire for a row that was carried over from
	// the OLD table by the migration itself, not only for a fresh one -- the
	// migrated row is the one a real upgrade actually contains.
	_, err = db.db.Exec(`DELETE FROM backup_runs WHERE id = 'run-legacy'`)
	require.NoError(t, err)
	var legacyCascadeCount int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM backup_run_items WHERE id = 'item-legacy'`).Scan(&legacyCascadeCount))
	assert.Equal(t, 0, legacyCascadeCount, "ON DELETE CASCADE must fire for a row migrated over from the old table")

	// 'interrupted' must now be accepted.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at) VALUES ('run-int', 'backup', 'manual', 'interrupted', '2026-02-01T00:00:00Z')`)
	require.NoError(t, err, "'interrupted' must be accepted after the migration widens the CHECK constraint")

	// The FK must still be live post-migration against the REBUILT parent: a
	// new child insert must succeed (this is what fails with "no such table"
	// under the naive rename-based rebuild), and ON DELETE CASCADE must fire
	// for this freshly-inserted row too.
	_, err = db.db.Exec(`INSERT INTO backup_run_items (id, run_id, stack_id, status) VALUES ('item-new', 'run-int', 'stacks~a', 'success')`)
	require.NoError(t, err, "a new backup_run_items insert against the rebuilt parent must succeed")

	_, err = db.db.Exec(`DELETE FROM backup_runs WHERE id = 'run-int'`)
	require.NoError(t, err)
	var cascadeCount int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM backup_run_items WHERE id = 'item-new'`).Scan(&cascadeCount))
	assert.Equal(t, 0, cascadeCount, "ON DELETE CASCADE must still fire against the rebuilt parent")

	// Indexes must be recreated, not silently dropped by the rebuild.
	rows, err := db.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'backup_runs'`)
	require.NoError(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	assert.Contains(t, names, "idx_backup_runs_started_at")
	assert.Contains(t, names, "idx_backup_runs_kind")

	// backup_run_items is also rebuilt (to detach it from backup_runs before
	// the parent is dropped, see the migration's comment) so its indexes must
	// be recreated too.
	itemRows, err := db.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'backup_run_items'`)
	require.NoError(t, err)
	defer itemRows.Close()
	var itemNames []string
	for itemRows.Next() {
		var name string
		require.NoError(t, itemRows.Scan(&name))
		itemNames = append(itemNames, name)
	}
	assert.Contains(t, itemNames, "idx_backup_run_items_run_id")
	assert.Contains(t, itemNames, "idx_backup_run_items_stack_id")
}

// applyMigrationsThrough applies migrations 1..throughVersion (inclusive) by
// driving the real Migration entries (PreCheck + SQL) through the same
// begin/PreCheck/Exec/record/commit sequence RunMigrations uses, but stops
// before a later version so a test can seed data in the gap -- e.g. inserting
// pre-existing case-colliding usernames after v12 (users exists, unchanged
// shape since v1) but before v13 (the NOCASE index) runs. This is not a
// hand-rolled schema probe: it is the actual migration definitions and the
// actual apply sequence, just checkpointed partway through, which is what
// the v12 test comment warns is required to see FK/constraint hazards that
// only show up under real transactional execution.
func applyMigrationsThrough(t *testing.T, db *DB, throughVersion int) {
	t.Helper()
	_, err := db.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)

	for _, m := range migrations {
		if m.Version > throughVersion {
			break
		}
		tx, err := db.db.Begin()
		require.NoError(t, err)
		if m.PreCheck != nil {
			require.NoError(t, m.PreCheck(tx))
		}
		_, err = tx.Exec(m.SQL)
		require.NoError(t, err)
		_, err = tx.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, CURRENT_TIMESTAMP)", m.Version)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
}

// TestMigration_UsernameNocase_DetectsCollisionAndAborts pins the required
// behavior from the decision note: a pre-existing pair of usernames that
// differ only by case must abort migration 13 with a named, actionable
// error -- not the bare "UNIQUE constraint failed" that CREATE UNIQUE INDEX
// would produce unaided -- and must leave the schema untouched so a retry
// after the operator fixes the data starts clean.
func TestMigration_UsernameNocase_DetectsCollisionAndAborts(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 12)

	_, err = db.db.Exec(`INSERT INTO users (id, username, password) VALUES ('u1', 'Alice', 'h')`)
	require.NoError(t, err)
	_, err = db.db.Exec(`INSERT INTO users (id, username, password) VALUES ('u2', 'alice', 'h')`)
	require.NoError(t, err)

	err = RunMigrations(db)
	require.Error(t, err, "migration 13 must abort when case-colliding usernames already exist")
	assert.Contains(t, err.Error(), "Alice", "the error must name the actual colliding usernames")
	assert.Contains(t, err.Error(), "alice", "the error must name the actual colliding usernames")

	var recorded int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 13`).Scan(&recorded))
	assert.Equal(t, 0, recorded, "migration 13 must not be recorded as applied when it aborted")

	var idxCount int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_users_username_nocase'`).Scan(&idxCount))
	assert.Equal(t, 0, idxCount, "the NOCASE index must not exist when the pre-check aborted the migration")

	// Both colliding rows must survive untouched -- an abort must not delete
	// or alter data, only refuse to proceed.
	var userCount int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount))
	assert.Equal(t, 2, userCount, "an aborted migration must not delete the colliding rows")
}

// TestMigration_UsernameNocase_NoCollision_EnforcesGoingForward is the
// positive case: with no pre-existing collision, migration 13 must apply
// cleanly and the resulting index must reject a new case-only duplicate.
func TestMigration_UsernameNocase_NoCollision_EnforcesGoingForward(t *testing.T) {
	db := newTestDB(t)

	_, err := db.db.Exec(`INSERT INTO users (id, username, password) VALUES ('u1', 'Bob', 'h')`)
	require.NoError(t, err)

	_, err = db.db.Exec(`INSERT INTO users (id, username, password) VALUES ('u2', 'bob', 'h')`)
	require.Error(t, err, "the NOCASE unique index created by migration 13 must reject a case-only duplicate")

	var recorded int
	require.NoError(t, db.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 13`).Scan(&recorded))
	assert.Equal(t, 1, recorded, "migration 13 must be recorded as applied when there is no collision")
}

// TestRunMigrations_NewerSchemaVersion_Refuses is the regression pin for the
// forward-version guard (agent-os-99j). This is the state left behind when
// an operator rolls back to an older image/tag to recover from a bad
// release -- the deployment story's :latest tag and watchtower label
// actively invite exactly this move -- and RunMigrations must not proceed
// as if nothing is wrong: rolling back across a migration can corrupt data.
func TestRunMigrations_NewerSchemaVersion_Refuses(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	require.NoError(t, RunMigrations(db))

	latest := migrations[len(migrations)-1].Version
	future := latest + 1
	_, err = db.db.Exec(
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, CURRENT_TIMESTAMP)", future,
	)
	require.NoError(t, err)

	err = RunMigrations(db)
	require.Error(t, err, "must refuse to start against a schema version newer than this binary understands")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d", future), "error must name the database's (newer) version")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d", latest), "error must name the binary's (older) version")
}

// TestRunMigrations_NewerSchemaVersion_AllowDowngradeEnv_ContinuesWithWarning
// covers the documented escape hatch: rollback is the operator's documented
// recovery move for a bad release, so the refusal above must not be a dead
// end once the operator has confirmed the specific rollback is safe.
func TestRunMigrations_NewerSchemaVersion_AllowDowngradeEnv_ContinuesWithWarning(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	require.NoError(t, RunMigrations(db))

	latest := migrations[len(migrations)-1].Version
	future := latest + 1
	_, err = db.db.Exec(
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, CURRENT_TIMESTAMP)", future,
	)
	require.NoError(t, err)

	t.Setenv(allowSchemaDowngradeEnv, "1")

	err = RunMigrations(db)
	require.NoError(t, err, "the override must downgrade the refusal to a warning and let startup continue")
}

// TestRunMigrations_FreshDatabase_DoesNotTripGuard pins the fresh-install
// case the guard must never break (agent-os-99j): a brand-new database has
// no schema_migrations rows at all on the first RunMigrations call, and that
// must migrate forward normally -- not be misread as "newer than the
// binary" and refused, which would turn a rollback-safety feature into a
// first-run outage.
func TestRunMigrations_FreshDatabase_DoesNotTripGuard(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	require.NoError(t, RunMigrations(db))

	latest := migrations[len(migrations)-1].Version
	var recorded int
	require.NoError(t, db.db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", latest,
	).Scan(&recorded))
	assert.Equal(t, 1, recorded, "a fresh database must migrate all the way to the binary's latest version")
}

// TestRunMigrations_PreVersionStampDatabase_DoesNotTripGuard covers the other
// half of the "must not brick" requirement: a schema_migrations table that
// already exists but holds zero rows -- the shape a database created by a
// binary predating any version stamp would have, since the table itself is
// (re)created idempotently by "CREATE TABLE IF NOT EXISTS" on every
// RunMigrations call regardless of what else the database already contains.
// MAX(version) over zero rows scans NULL, read as appliedVersion 0 -- the
// same value the brand-new case produces -- so this must also migrate
// forward normally, not refuse. The guard cannot and must not distinguish
// "unstamped legacy database" from "fresh database"; both are treated as
// "assume current, stamp it going forward".
func TestRunMigrations_PreVersionStampDatabase_DoesNotTripGuard(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = db.db.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)

	err = RunMigrations(db)
	require.NoError(t, err, "a pre-existing empty schema_migrations table must not be treated as newer than the binary")

	latest := migrations[len(migrations)-1].Version
	var recorded int
	require.NoError(t, db.db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", latest,
	).Scan(&recorded))
	assert.Equal(t, 1, recorded, "an unstamped legacy database must migrate all the way to the binary's latest version")
}
