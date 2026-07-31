package database

import (
	"database/sql"
	"fmt"
	"strings"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
}

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

			_, err = tx.Exec(migration.SQL)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d: %w", migration.Version, err)
			}

			_, err = tx.Exec(
				"INSERT INTO schema_migrations (version, applied_at) VALUES (?, CURRENT_TIMESTAMP)",
				migration.Version,
			)
			if err != nil {
				tx.Rollback()
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
	defer tx.Rollback()

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
