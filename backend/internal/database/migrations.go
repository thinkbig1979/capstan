package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// allowSchemaDowngradeEnv is the documented escape hatch for the forward-
// version guard in RunMigrations (agent-os-99j). Rollback is the operator's
// documented recovery move for a bad release (see README's Rollback
// section), so refusing outright with no way through would turn a
// rollback-safety feature into a dead end. Set to "1" only once the operator
// has confirmed the specific rollback is safe for the migrations involved --
// the guard cannot know that, it only knows the version numbers don't match.
const allowSchemaDowngradeEnv = "CAPSTAN_ALLOW_SCHEMA_DOWNGRADE"

type Migration struct {
	Version int
	Name    string
	SQL     string
	// PreCheck runs inside the same transaction as SQL, immediately before it,
	// and only when this migration has not yet been applied. It exists for
	// migrations that must validate pre-existing data before altering schema
	// and report a specific, actionable error rather than let a raw
	// constraint failure abort the migration (agent-os-tmo: a NOCASE unique
	// index built directly against pre-existing case-colliding usernames
	// fails with a bare "UNIQUE constraint failed", naming no rows).
	// SQLite's RAISE() can only be used inside a trigger body, not a plain
	// statement, so this kind of check cannot be expressed as SQL text here.
	// A non-nil error rolls back the transaction, leaving schema and
	// schema_migrations untouched, and propagates out of RunMigrations exactly
	// like any other migration failure. Nil for every migration that needs no
	// pre-check.
	PreCheck func(tx *sql.Tx) error
}

// Table rebuilds in SQLite: safe ordering (agent-os-xzq)
//
// SQLite has no ALTER COLUMN, ALTER CONSTRAINT, or DROP CONSTRAINT: widening
// a CHECK constraint, changing a column's type, or altering a foreign key
// definition all require rebuilding the table under a new CREATE TABLE and
// copying the data across. The obvious-looking approach --
//
//	ALTER TABLE parent RENAME TO parent_old;
//	CREATE TABLE parent (...);          -- new definition, original name
//	INSERT INTO parent SELECT ... FROM parent_old;
//	DROP TABLE parent_old;
//
// -- is what migration 9 (action_log_denormalized, below) uses, and it is
// safe THERE ONLY because nothing holds a foreign key referencing
// action_log. It is NOT safe for any table that has an incoming FK, and it
// fails silently at migration time, not loudly:
//
//   - ALTER TABLE parent RENAME TO parent_old makes SQLite automatically
//     rewrite the stored FK text of every CHILD table to follow the rename.
//     A child's `FOREIGN KEY (x) REFERENCES parent(id)` silently becomes
//     `FOREIGN KEY (x) REFERENCES "parent_old"(id)`.
//   - Creating a fresh `parent` (the new definition, under the original
//     name) and dropping `parent_old` then succeeds with NO ERROR, but the
//     child's FK now points at a table name that no longer exists.
//   - The first symptom is the next INSERT into the child table failing
//     with "no such table: main.parent_old" -- in production, on the first
//     write after the upgrade, naming a table nobody recognises.
//
// The safe ordering never renames OUT of the name a child FK refers to --
// it only ever renames INTO it:
//
//	CREATE TABLE parent_new (...);
//	INSERT INTO parent_new (explicit column list) SELECT ... FROM parent;
//	DROP TABLE parent;
//	ALTER TABLE parent_new RENAME TO parent;
//
// The child's FK text keeps pointing at "parent" throughout and resolves
// correctly the moment the rename lands.
//
// A second, easy-to-miss hazard applies even with the safe ordering: DROP
// TABLE parent while a child table still exists and still holds rows
// referencing it CASCADE-DELETES those rows during the drop -- but only
// when the DROP runs inside an explicit transaction with foreign_keys
// enforcement on, which is exactly how RunMigrations executes every
// migration (one multi-statement Exec inside a tx). An ad hoc probe run
// statement-by-statement and auto-committing does NOT reproduce this and
// will report a false "safe" verdict (agent-os-pid: this is exactly what
// happened on the first diagnostic attempt at migration 12, before the real
// runner was used). If the child table itself must also change shape,
// detach its data into a plain, FK-free table BEFORE the parent is touched
// at all, then drop the child (dropping a CHILD table is never a cascade
// hazard) before dropping the parent, rebuild+rename the parent, then
// rebuild the child from the detached copy.
//
// Migration 12 (backup_runs_interrupted_status, below) is the worked
// example of both hazards and their fix -- read its comment for the
// concrete SQL. TestMigration_BackupRunsInterruptedStatus_PreservesDataAndFK
// in migrations_test.go pins the result through the real migration runner,
// not a hand-assembled probe; assertRebuildForeignKeyIntact in that file is
// a reusable assertion any future rebuild migration's regression test
// should call.
//
// Decision on migration 9: left as-is. action_log has no incoming foreign
// key, so the rename-old/create-new-under-the-old-name/drop-old ordering it
// uses is correct as shipped. Rewriting a migration that has already run in
// the field is a strictly worse risk than a comment, so the fix here is
// documentation, not a schema change -- see the note at migration 9 itself,
// below.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial_schema",
		SQL: `
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT UNIQUE NOT NULL,
	password TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	expires_at TIMESTAMP NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS directories (
	path TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	is_git_repo BOOLEAN NOT NULL DEFAULT 0,
	git_remote TEXT,
	git_branch TEXT,
	scanned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS stacks (
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
	git_behind INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (directory) REFERENCES directories(path) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS action_log (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	stack_id TEXT,
	action TEXT NOT NULL,
	detail TEXT,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_stacks_directory ON stacks(directory);
CREATE INDEX IF NOT EXISTS idx_action_log_user_id ON action_log(user_id);
CREATE INDEX IF NOT EXISTS idx_action_log_stack_id ON action_log(stack_id);
CREATE INDEX IF NOT EXISTS idx_action_log_created_at ON action_log(created_at);
`,
	},
	{
		Version: 2,
		Name:    "default_log_retention",
		SQL: `
INSERT OR IGNORE INTO settings (key, value) VALUES ('max_log_retention_days', '90');
`,
	},
	{
		Version: 3,
		Name:    "update_scheduling_schema",
		SQL: `
CREATE TABLE IF NOT EXISTS cached_updates (
    id TEXT PRIMARY KEY,
    container_id TEXT NOT NULL,
    container_name TEXT NOT NULL,
    image TEXT NOT NULL,
    image_ref TEXT NOT NULL,
    state TEXT NOT NULL,
    stack_id TEXT,
    project_name TEXT,
    service_name TEXT,
    is_compose BOOLEAN NOT NULL DEFAULT FALSE,
    local_digest TEXT NOT NULL,
    remote_digest TEXT NOT NULL,
    scanned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cached_updates_container_id ON cached_updates(container_id);
CREATE INDEX IF NOT EXISTS idx_cached_updates_stack_id ON cached_updates(stack_id);

CREATE TABLE IF NOT EXISTS auto_update_policies (
    id TEXT PRIMARY KEY,
    target_type TEXT NOT NULL CHECK (target_type IN ('container', 'stack')),
    target_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    paused BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_auto_update_policies_target ON auto_update_policies(target_type, target_id);

CREATE TABLE IF NOT EXISTS update_history (
    id TEXT PRIMARY KEY,
    container_id TEXT NOT NULL,
    container_name TEXT NOT NULL,
    stack_id TEXT,
    stack_name TEXT,
    image TEXT NOT NULL,
    old_digest TEXT,
    new_digest TEXT,
    old_image_ref TEXT,
    new_image_ref TEXT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'success', 'failed', 'paused')),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'auto')),
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    duration_ms INTEGER,
    error_message TEXT
);

CREATE INDEX IF NOT EXISTS idx_update_history_container_id ON update_history(container_id);
CREATE INDEX IF NOT EXISTS idx_update_history_stack_id ON update_history(stack_id);
CREATE INDEX IF NOT EXISTS idx_update_history_status ON update_history(status);
CREATE INDEX IF NOT EXISTS idx_update_history_trigger ON update_history(trigger);
CREATE INDEX IF NOT EXISTS idx_update_history_started_at ON update_history(started_at);

INSERT OR IGNORE INTO settings (key, value) VALUES ('update_scan_interval', '0');
INSERT OR IGNORE INTO settings (key, value) VALUES ('auto_update_enabled', 'false');
`,
	},
	{
		Version: 4,
		Name:    "directory_git_credentials",
		SQL: `
ALTER TABLE directories ADD COLUMN git_auth_type TEXT NOT NULL DEFAULT '';
ALTER TABLE directories ADD COLUMN git_ssh_key_path TEXT NOT NULL DEFAULT '';
ALTER TABLE directories ADD COLUMN git_https_user TEXT NOT NULL DEFAULT '';
ALTER TABLE directories ADD COLUMN git_https_token TEXT NOT NULL DEFAULT '';
`,
	},
	{
		Version: 5,
		Name:    "stacks_directories_setting",
		SQL: `
INSERT OR IGNORE INTO settings (key, value) VALUES ('default_stacks_dir', '');
ALTER TABLE directories ADD COLUMN root_dir TEXT NOT NULL DEFAULT '';
`,
	},
	{
		Version: 6,
		Name:    "stack_id_root_prefix_marker",
		SQL: `
INSERT OR IGNORE INTO settings (key, value) VALUES ('stack_id_version', '1');
`,
	},
	{
		Version: 7,
		Name:    "scan_depth_setting",
		SQL: `
INSERT OR IGNORE INTO settings (key, value) VALUES ('scan_depth', '1');
`,
	},
	{
		Version: 8,
		Name:    "backup_engine_schema",
		SQL: `
CREATE TABLE IF NOT EXISTS backup_policies (
    id            TEXT PRIMARY KEY,
    target_type   TEXT NOT NULL CHECK (target_type IN ('stack')),
    target_id     TEXT NOT NULL,
    enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    stop_policy   TEXT NOT NULL DEFAULT 'stop'   CHECK (stop_policy IN ('stop','hot')),
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(target_type, target_id)
);

CREATE TABLE IF NOT EXISTS backup_runs (
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

CREATE INDEX IF NOT EXISTS idx_backup_runs_started_at ON backup_runs(started_at);
CREATE INDEX IF NOT EXISTS idx_backup_runs_kind ON backup_runs(kind);

CREATE TABLE IF NOT EXISTS backup_run_items (
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

CREATE INDEX IF NOT EXISTS idx_backup_run_items_run_id ON backup_run_items(run_id);
CREATE INDEX IF NOT EXISTS idx_backup_run_items_stack_id ON backup_run_items(stack_id);
`,
	},
	{
		Version: 9,
		Name:    "action_log_denormalized",
		SQL: `
-- action_log becomes a denormalized, append-only audit record: user_id and
-- stack_id are no longer foreign keys to users/stacks. Both FKs were
-- ON DELETE CASCADE, so deleting a user or a stack silently erased the
-- history of what they did -- the opposite of what an audit log is for.
-- Sentinel actor labels such as "anonymous" (AUTH_DISABLED mode) or "system"
-- (background jobs) are legitimate values here, not placeholders that need
-- to resolve to a real row, so existing data is copied verbatim.
--
-- NOTE (agent-os-xzq): the rename-old/create-new-under-the-old-name/drop-old
-- ordering below is UNSAFE for any table with an incoming foreign key -- see
-- the "Table rebuilds in SQLite" comment at the top of this file for why
-- (SQLite silently rewrites the child's FK text on the rename, then the drop
-- leaves it pointing at a table that no longer exists). It is safe HERE, and
-- only here, because nothing references action_log. DECISION: left as-is;
-- rewriting a migration that has already run in the field is a strictly
-- worse risk than a comment.
ALTER TABLE action_log RENAME TO action_log_v8;

CREATE TABLE action_log (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	stack_id TEXT,
	action TEXT NOT NULL,
	detail TEXT,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO action_log (id, user_id, stack_id, action, detail, created_at)
SELECT id, user_id, stack_id, action, detail, created_at
FROM action_log_v8;

DROP TABLE action_log_v8;

CREATE INDEX IF NOT EXISTS idx_action_log_user_id ON action_log(user_id);
CREATE INDEX IF NOT EXISTS idx_action_log_stack_id ON action_log(stack_id);
CREATE INDEX IF NOT EXISTS idx_action_log_created_at ON action_log(created_at);
`,
	},
	{
		Version: 10,
		Name:    "action_log_request_id",
		SQL: `
-- Carries the per-request ID (see middleware.RequestID) onto the audit row, so
-- a 500 in the HTTP log can be joined to the action it produced. Nullable: rows
-- written before this migration, and by background jobs that serve no request,
-- legitimately have none.
ALTER TABLE action_log ADD COLUMN request_id TEXT;

CREATE INDEX IF NOT EXISTS idx_action_log_request_id ON action_log(request_id);
`,
	},
	{
		Version: 11,
		Name:    "history_retention_settings",
		SQL: `
-- Retention for the two history tables that previously grew without bound.
-- Defaults match max_log_retention_days so all three behave the same; the
-- floor is enforced in code (database.RetentionDays), not here, so an operator
-- editing the row directly cannot bypass it either.
INSERT OR IGNORE INTO settings (key, value) VALUES ('max_update_history_retention_days', '90');
INSERT OR IGNORE INTO settings (key, value) VALUES ('max_backup_history_retention_days', '90');

-- update_history was only ever queried by completed_at through a full scan.
CREATE INDEX IF NOT EXISTS idx_update_history_completed_at ON update_history(completed_at);
`,
	},
	{
		Version: 12,
		Name:    "backup_runs_interrupted_status",
		SQL: `
-- backup_runs.status gains 'interrupted', distinct from 'failed' (agent-os-pid).
-- A run whose process crashed, or whose database was restored from a snapshot
-- taken mid-run, never reported a real outcome -- it may well have SUCCEEDED
-- on the original instance. Reusing 'failed' would tell an operator who just
-- recovered from an outage, in the dashboard's most prominent badge, that
-- their backup failed when it did not. SQLite has no ALTER COLUMN for CHECK
-- constraints, so widening the allowed set means rebuilding the table.
--
-- This rebuild is more involved than migration 9's (action_log, which has no
-- incoming foreign key). backup_run_items.run_id is
-- FOREIGN KEY (run_id) REFERENCES backup_runs(id) ON DELETE CASCADE, and two
-- separate hazards were found and verified empirically before writing this:
--
--  1. Renaming backup_runs itself (migration 9's rename-old / create-new-
--     under-the-old-name / drop-old pattern) makes SQLite rewrite
--     backup_run_items' stored FK text to point at the renamed name; dropping
--     that renamed table then leaves the FK referencing a name that no
--     longer exists, so every subsequent INSERT into backup_run_items fails
--     with "no such table". Avoided here by renaming the NEW table INTO the
--     original "backup_runs" name instead, so backup_run_items' FK text
--     never changes.
--  2. Even with that fixed, DROP TABLE backup_runs while backup_run_items
--     still exists and still holds rows referencing it CASCADE-DELETES those
--     rows during the drop -- but only when the DROP runs inside an explicit
--     transaction with foreign_keys enforcement on, which is exactly how
--     RunMigrations executes every migration (one multi-statement Exec
--     inside a tx). Auto-committing statement-by-statement, as in an ad hoc
--     shell/probe, does NOT reproduce this; it only shows up under the real
--     migration-runner execution path, which is why it is called out here
--     explicitly and pinned by a test that runs migrations for real rather
--     than only inspecting the resulting schema.
--
-- The fix for #2: detach backup_run_items' data into a plain, constraint-free
-- table BEFORE backup_runs is touched at all, then drop backup_run_items
-- itself (dropping a CHILD table is never a cascade hazard) before dropping
-- backup_runs. By the time backup_runs is dropped, no table with a live FK
-- to it exists, so there is nothing to cascade into.
CREATE TABLE backup_run_items_v11 AS SELECT * FROM backup_run_items;

CREATE TABLE backup_runs_new (
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

INSERT INTO backup_runs_new (id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message)
SELECT id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message
FROM backup_runs;

DROP TABLE backup_run_items;
DROP TABLE backup_runs;

ALTER TABLE backup_runs_new RENAME TO backup_runs;

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

INSERT INTO backup_run_items (id, run_id, stack_id, status, snapshot_id, stop_applied, duration_ms, error_message)
SELECT id, run_id, stack_id, status, snapshot_id, stop_applied, duration_ms, error_message
FROM backup_run_items_v11;

DROP TABLE backup_run_items_v11;

CREATE INDEX IF NOT EXISTS idx_backup_runs_started_at ON backup_runs(started_at);
CREATE INDEX IF NOT EXISTS idx_backup_runs_kind ON backup_runs(kind);
CREATE INDEX IF NOT EXISTS idx_backup_run_items_run_id ON backup_run_items(run_id);
CREATE INDEX IF NOT EXISTS idx_backup_run_items_stack_id ON backup_run_items(stack_id);
`,
	},
	{
		Version: 13,
		Name:    "users_username_nocase_unique",
		// Usernames become case-insensitive at the DB layer (agent-os-tmo).
		// Without this, database.GetUserByUsername compares under SQLite's
		// default BINARY collation, so "Admin" and "admin" are distinct
		// accounts -- indistinguishable in most UI, and (unlike the login
		// rate limiter's normalizeAccount, which already folds case for a
		// different reason, see middleware/ratelimit.go) not what the DB
		// itself enforces.
		//
		// This is an index, not a users-table rebuild, deliberately: users
		// and sessions are the two tables where a botched migration means
		// nobody can log in at all, and sessions.user_id has a live
		// ON DELETE CASCADE FK to users(id) (migrations.go v1) that would
		// hit the same two hazards migration 12's comment documents if users
		// were dropped/renamed. A NOCASE index sidesteps both -- verified
		// empirically (agent-os-tmo) that (a) the index enforces on INSERT
		// regardless of the inserting statement's own collation, (b) a query
		// with `COLLATE NOCASE` on the predicate uses the index (EXPLAIN
		// QUERY PLAN: SEARCH ... USING INDEX idx_users_username_nocase), and
		// (c) the pre-existing binary-collation UNIQUE on the column does
		// not conflict with it.
		//
		// PreCheck (below) is required, not optional: creating this index
		// directly against a database that already holds two usernames
		// differing only by case fails with a bare "UNIQUE constraint
		// failed", naming no rows -- exactly the "dying on a unique index"
		// outcome this migration must not produce. PreCheck runs first and
		// aborts with the actual colliding usernames named, leaving schema
		// untouched, so an operator can resolve it by hand.
		PreCheck: checkNoCaseCollidingUsernames,
		SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_nocase ON users(username COLLATE NOCASE);
`,
	},
	{
		Version: 14,
		Name:    "schedule_settings",
		SQL: `
-- Defaults for the scheduled-update window (agent-os-mtbo). 'immediate'
-- reproduces today's behaviour exactly: updates apply as soon as they are
-- approved, and the time/days rows are inert until the mode is changed, so
-- upgrading an existing install changes nothing until an operator opts in.
--
-- INSERT OR IGNORE, not INSERT OR REPLACE: an operator who has already set a
-- window must keep it across every subsequent restart. Unlike the backup
-- settings, these three have no environment-variable fallback to shadow --
-- nothing in internal/config reads an UPDATE_APPLY_* variable -- so seeding
-- them cannot make an env var silently dead the way seeding a backup_ key
-- would (services.resolveIntSetting prefers a non-empty DB row over the env).
INSERT OR IGNORE INTO settings (key, value) VALUES ('update_apply_mode', 'immediate');
INSERT OR IGNORE INTO settings (key, value) VALUES ('update_apply_time', '03:00');
INSERT OR IGNORE INTO settings (key, value) VALUES ('update_apply_days', '0,1,2,3,4,5,6');
`,
	},
}

// checkNoCaseCollidingUsernames is migration 13's PreCheck. It detects
// usernames that differ only by case -- which the pre-v13 schema's BINARY
// collation allowed to coexist as two distinct accounts -- and fails with
// their exact values rather than let the CREATE UNIQUE INDEX below abort
// with a bare constraint-violation error that names nothing. Case-only
// collisions cannot arise through the application itself: CreateUser
// (database/users.go) has exactly one caller, AuthHandler.Setup, which
// refuses once a single user exists, so this can only fire against a
// database that was altered outside the app (manual SQL, a restored/merged
// backup). That is precisely when an operator needs an explicit stop, not a
// migration that silently renames or drops one of the colliding accounts.
func checkNoCaseCollidingUsernames(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT username FROM users
		WHERE LOWER(username) IN (
			SELECT LOWER(username) FROM users GROUP BY LOWER(username) HAVING COUNT(*) > 1
		)
		ORDER BY LOWER(username), username
	`)
	if err != nil {
		return fmt.Errorf("checking for case-colliding usernames: %w", err)
	}
	defer rows.Close()

	var colliding []string
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return fmt.Errorf("scanning colliding username: %w", err)
		}
		colliding = append(colliding, username)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading colliding usernames: %w", err)
	}

	if len(colliding) > 0 {
		return fmt.Errorf(
			"cannot make usernames case-insensitive: %d existing username(s) collide only by case and must be resolved by hand before restarting: %s",
			len(colliding), strings.Join(colliding, ", "),
		)
	}
	return nil
}

func RunMigrations(db *DB) error {
	_, err := db.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Forward-version guard (agent-os-99j): detect a database that was
	// migrated by a NEWER binary than this one -- the situation created when
	// an operator rolls back to an older image/tag to recover from a bad
	// release, which the :latest tag and watchtower both actively invite.
	// Rolling back across a migration can corrupt data, so an unrecognized
	// future version must not be applied against silently.
	//
	// MAX(version) over an empty schema_migrations table scans NULL, which
	// Scan below reads as appliedVersion == 0 via sql.NullInt64's zero value.
	// That 0 is indistinguishable from -- and must be treated the same as --
	// a genuinely fresh database that has never been migrated: 0 is never
	// greater than latestKnownVersion, so neither a brand-new database nor a
	// database created before version-stamping existed trips the guard. Both
	// fall through to "assume current, migrate forward from scratch", not
	// "unknown, refuse" -- refusing here would convert a rollback-safety
	// feature into a first-run outage.
	var appliedVersion sql.NullInt64
	if err := db.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&appliedVersion); err != nil {
		return fmt.Errorf("failed to read schema_migrations max version: %w", err)
	}
	dbVersion := int(appliedVersion.Int64)
	latestKnownVersion := migrations[len(migrations)-1].Version

	// Logged unconditionally, on every startup, not just when the guard
	// trips -- the schema version being invisible in normal operation is
	// half of why this went unnoticed in the first place.
	slog.Info("database schema version", "database_version", dbVersion, "binary_version", latestKnownVersion)

	if dbVersion > latestKnownVersion {
		if os.Getenv(allowSchemaDowngradeEnv) == "1" {
			// Safe, not just permissive: the apply loop below only ever
			// runs a migration when its own version scans sql.ErrNoRows,
			// and every version up to dbVersion is already recorded here
			// by definition. So letting a newer database through does not
			// re-run or re-apply anything against it -- every one of this
			// binary's known migrations is skipped as already-applied,
			// and the loop falls straight through to returning nil.
			slog.Warn(
				"database schema is newer than this binary understands; continuing because "+allowSchemaDowngradeEnv+"=1",
				"database_version", dbVersion, "binary_version", latestKnownVersion,
			)
		} else {
			return fmt.Errorf(
				"FATAL: database schema version %d is newer than this binary understands (%d).\n"+
					"This image is older than the one that last migrated the database.\n"+
					"Rolling back across a migration can corrupt data.\n"+
					"If you know this rollback is safe, set: %s=1",
				dbVersion, latestKnownVersion, allowSchemaDowngradeEnv,
			)
		}
	}

	for _, migration := range migrations {
		var appliedAt string
		err := db.db.QueryRow(
			"SELECT applied_at FROM schema_migrations WHERE version = ?",
			migration.Version,
		).Scan(&appliedAt)

		if err == sql.ErrNoRows {
			tx, err := db.db.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin transaction for migration %d: %w", migration.Version, err)
			}

			if migration.PreCheck != nil {
				if err := migration.PreCheck(tx); err != nil {
					// Rollback error is secondary to the pre-check error
					// already being returned; the tx is abandoned either way.
					_ = tx.Rollback()
					return fmt.Errorf("migration %d pre-check failed: %w", migration.Version, err)
				}
			}

			_, err = tx.Exec(migration.SQL)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed to apply migration %d: %w", migration.Version, err)
			}

			_, err = tx.Exec(
				"INSERT INTO schema_migrations (version, applied_at) VALUES (?, CURRENT_TIMESTAMP)",
				migration.Version,
			)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
			}

			err = tx.Commit()
			if err != nil {
				return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to check migration %d status: %w", migration.Version, err)
		}
	}

	return nil
}

func (d *DB) MigrateStackIDsToRootPrefixed(stacksDir string) error {
	version, err := d.GetSetting("stack_id_version")
	if err != nil {
		return fmt.Errorf("checking stack_id_version: %w", err)
	}
	if version == "2" {
		return nil
	}

	rootPrefix := ""
	if stacksDir != "" {
		idx := strings.LastIndex(stacksDir, "/")
		if idx >= 0 {
			rootPrefix = stacksDir[idx+1:]
		} else {
			rootPrefix = stacksDir
		}
	}
	if rootPrefix == "" {
		rootPrefix = "stacks"
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// No-op once Commit succeeds (sql.ErrTxDone); the safety net for the
	// early-return error paths below is what matters.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("UPDATE directories SET root_dir = ? WHERE root_dir = ''", stacksDir); err != nil {
		return fmt.Errorf("update directory root_dir: %w", err)
	}

	rows, err := tx.Query(`SELECT id, directory FROM stacks`)
	if err != nil {
		return fmt.Errorf("query stacks: %w", err)
	}
	defer rows.Close()

	type idMapping struct {
		oldID string
		newID string
		dir   string
	}
	var mappings []idMapping

	for rows.Next() {
		var id, dir string
		if err := rows.Scan(&id, &dir); err != nil {
			return fmt.Errorf("scan stack: %w", err)
		}

		if strings.Contains(id, "~") {
			continue
		}

		newID := rootPrefix + "~" + id
		mappings = append(mappings, idMapping{oldID: id, newID: newID, dir: dir})
	}
	rows.Close()

	for _, m := range mappings {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO stacks
			(id, directory, compose_file, env_file, project_name, status,
			 is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind)
			SELECT ?, directory, compose_file, env_file, project_name, status,
			 is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind
			FROM stacks WHERE id = ?`, m.newID, m.oldID); err != nil {
			return fmt.Errorf("insert new stack %s: %w", m.newID, err)
		}
		if _, err := tx.Exec(`DELETE FROM stacks WHERE id = ?`, m.oldID); err != nil {
			return fmt.Errorf("delete old stack %s: %w", m.oldID, err)
		}
		if _, err := tx.Exec(`UPDATE action_log SET stack_id = ? WHERE stack_id = ?`, m.newID, m.oldID); err != nil {
			return fmt.Errorf("update action_log %s: %w", m.oldID, err)
		}
		if _, err := tx.Exec(`UPDATE cached_updates SET stack_id = ? WHERE stack_id = ?`, m.newID, m.oldID); err != nil {
			return fmt.Errorf("update cached_updates %s: %w", m.oldID, err)
		}
		if _, err := tx.Exec(`UPDATE update_history SET stack_id = ? WHERE stack_id = ?`, m.newID, m.oldID); err != nil {
			return fmt.Errorf("update update_history %s: %w", m.oldID, err)
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('stack_id_version', '2')`); err != nil {
		return fmt.Errorf("update stack_id_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if len(mappings) > 0 {
		fmt.Printf("Migrated %d stack IDs to root-prefixed format (prefix=%s)\n", len(mappings), rootPrefix)
	}

	return nil
}
