package database

import (
	"database/sql"
	"fmt"
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
