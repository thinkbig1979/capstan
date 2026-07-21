package database

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

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
