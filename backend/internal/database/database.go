package database

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"github.com/docker-manager/backend/internal/models"
)

type DB struct {
	db *sql.DB
}

func New(dataDir string) (*DB, error) {
	dbPath := dataDir + "/docker-manager.db"

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	_, err = db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec("PRAGMA foreign_keys=ON")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec("PRAGMA busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) UserCount() (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (d *DB) CreateUser(user models.User) error {
	query := `INSERT INTO users (id, username, password, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, user.ID, user.Username, user.Password, user.CreatedAt, user.UpdatedAt)
	return err
}

func (d *DB) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	query := `SELECT id, username, password, created_at, updated_at
	          FROM users WHERE username = ?`
	err := d.db.QueryRow(query, username).Scan(&user.ID, &user.Username, &user.Password, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) GetUserByID(id string) (*models.User, error) {
	var user models.User
	query := `SELECT id, username, password, created_at, updated_at
	          FROM users WHERE id = ?`
	err := d.db.QueryRow(query, id).Scan(&user.ID, &user.Username, &user.Password, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) UpdateUserPassword(id, password string, updatedAt time.Time) error {
	query := `UPDATE users SET password = ?, updated_at = ? WHERE id = ?`
	_, err := d.db.Exec(query, password, updatedAt, id)
	return err
}

func (d *DB) CreateSession(session models.Session) error {
	query := `INSERT INTO sessions (id, user_id, expires_at, created_at)
	          VALUES (?, ?, ?, ?)`
	_, err := d.db.Exec(query, session.ID, session.UserID, session.ExpiresAt, session.CreatedAt)
	return err
}

func (d *DB) GetSession(id string) (*models.Session, error) {
	var session models.Session
	query := `SELECT id, user_id, expires_at, created_at
	          FROM sessions WHERE id = ?`
	err := d.db.QueryRow(query, id).Scan(&session.ID, &session.UserID, &session.ExpiresAt, &session.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (d *DB) DeleteSession(id string) error {
	query := `DELETE FROM sessions WHERE id = ?`
	_, err := d.db.Exec(query, id)
	return err
}

func (d *DB) DeleteExpiredSessions() error {
	query := `DELETE FROM sessions WHERE expires_at < ?`
	_, err := d.db.Exec(query, time.Now())
	return err
}

func (d *DB) UpsertDirectory(dir models.Directory) error {
	query := `INSERT OR REPLACE INTO directories
	          (path, name, is_git_repo, git_remote, git_branch, scanned_at)
	          VALUES (?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, dir.Path, dir.Name, dir.IsGitRepo, dir.GitRemote, dir.GitBranch, dir.ScannedAt)
	return err
}

func (d *DB) ListDirectories() ([]models.Directory, error) {
	query := `SELECT path, name, is_git_repo, git_remote, git_branch, scanned_at
	          FROM directories ORDER BY name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	directories := make([]models.Directory, 0)
	for rows.Next() {
		var dir models.Directory
		err := rows.Scan(&dir.Path, &dir.Name, &dir.IsGitRepo, &dir.GitRemote, &dir.GitBranch, &dir.ScannedAt)
		if err != nil {
			return nil, err
		}
		directories = append(directories, dir)
	}
	return directories, nil
}

func (d *DB) GetDirectory(path string) (*models.Directory, error) {
	var dir models.Directory
	query := `SELECT path, name, is_git_repo, git_remote, git_branch, scanned_at
	          FROM directories WHERE path = ?`
	err := d.db.QueryRow(query, path).Scan(&dir.Path, &dir.Name, &dir.IsGitRepo, &dir.GitRemote, &dir.GitBranch, &dir.ScannedAt)
	if err != nil {
		return nil, err
	}
	return &dir, nil
}

func (d *DB) DeleteDirectory(path string) error {
	query := `DELETE FROM directories WHERE path = ?`
	_, err := d.db.Exec(query, path)
	return err
}

func (d *DB) ClearDirectories() error {
	query := `DELETE FROM directories`
	_, err := d.db.Exec(query)
	return err
}

func (d *DB) UpsertStack(stack models.Stack) error {
	query := `INSERT OR REPLACE INTO stacks
	          (id, directory, compose_file, env_file, project_name, status,
	           is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, stack.ID, stack.Directory, stack.ComposeFile, stack.EnvFile,
		stack.ProjectName, stack.Status, stack.IsGitRepo, stack.GitBranch, stack.GitCommit,
		stack.GitDirty, stack.GitAhead, stack.GitBehind)
	return err
}

func (d *DB) ListStacks() ([]models.Stack, error) {
	query := `SELECT id, directory, compose_file, env_file, project_name, status,
	           is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind
	          FROM stacks ORDER BY project_name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stacks := make([]models.Stack, 0)
	for rows.Next() {
		var stack models.Stack
		err := rows.Scan(&stack.ID, &stack.Directory, &stack.ComposeFile, &stack.EnvFile,
			&stack.ProjectName, &stack.Status, &stack.IsGitRepo, &stack.GitBranch,
			&stack.GitCommit, &stack.GitDirty, &stack.GitAhead, &stack.GitBehind)
		if err != nil {
			return nil, err
		}
		stacks = append(stacks, stack)
	}
	return stacks, nil
}

func (d *DB) GetStack(id string) (*models.Stack, error) {
	var stack models.Stack
	query := `SELECT id, directory, compose_file, env_file, project_name, status,
	           is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind
	          FROM stacks WHERE id = ?`
	err := d.db.QueryRow(query, id).Scan(&stack.ID, &stack.Directory, &stack.ComposeFile, &stack.EnvFile,
		&stack.ProjectName, &stack.Status, &stack.IsGitRepo, &stack.GitBranch,
		&stack.GitCommit, &stack.GitDirty, &stack.GitAhead, &stack.GitBehind)
	if err != nil {
		return nil, err
	}
	return &stack, nil
}

func (d *DB) ListStacksByDirectory(path string) ([]models.Stack, error) {
	query := `SELECT id, directory, compose_file, env_file, project_name, status,
	           is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind
	          FROM stacks WHERE directory = ? ORDER BY project_name`
	rows, err := d.db.Query(query, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stacks := make([]models.Stack, 0)
	for rows.Next() {
		var stack models.Stack
		err := rows.Scan(&stack.ID, &stack.Directory, &stack.ComposeFile, &stack.EnvFile,
			&stack.ProjectName, &stack.Status, &stack.IsGitRepo, &stack.GitBranch,
			&stack.GitCommit, &stack.GitDirty, &stack.GitAhead, &stack.GitBehind)
		if err != nil {
			return nil, err
		}
		stacks = append(stacks, stack)
	}
	return stacks, nil
}

func (d *DB) DeleteStack(id string) error {
	query := `DELETE FROM stacks WHERE id = ?`
	_, err := d.db.Exec(query, id)
	return err
}

func (d *DB) ClearStacks() error {
	query := `DELETE FROM stacks`
	_, err := d.db.Exec(query)
	return err
}

func (d *DB) UpdateStackStatus(id, status string) error {
	query := `UPDATE stacks SET status = ? WHERE id = ?`
	_, err := d.db.Exec(query, status, id)
	return err
}

func (d *DB) GetStackByProjectName(projectName string) (*models.Stack, error) {
	var stack models.Stack
	query := `SELECT id, directory, compose_file, env_file, project_name, status,
	           is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind
	          FROM stacks WHERE project_name = ?`
	err := d.db.QueryRow(query, projectName).Scan(&stack.ID, &stack.Directory, &stack.ComposeFile, &stack.EnvFile,
		&stack.ProjectName, &stack.Status, &stack.IsGitRepo, &stack.GitBranch,
		&stack.GitCommit, &stack.GitDirty, &stack.GitAhead, &stack.GitBehind)
	if err != nil {
		return nil, err
	}
	return &stack, nil
}

func (d *DB) LogAction(log models.ActionLog) error {
	query := `INSERT INTO action_log (id, user_id, stack_id, action, detail, created_at)
	          VALUES (?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, log.ID, log.UserID, log.StackID, log.Action, log.Detail, log.CreatedAt)
	return err
}

func (d *DB) GetActionsByStack(stackID string, limit int) ([]models.ActionLog, error) {
	query := `SELECT id, user_id, stack_id, action, detail, created_at
	          FROM action_log WHERE stack_id = ? ORDER BY created_at DESC LIMIT ?`
	rows, err := d.db.Query(query, stackID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		var action models.ActionLog
		err := rows.Scan(&action.ID, &action.UserID, &action.StackID, &action.Action, &action.Detail, &action.CreatedAt)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (d *DB) GetRecentActions(limit int) ([]models.ActionLog, error) {
	query := `SELECT id, user_id, stack_id, action, detail, created_at
	          FROM action_log ORDER BY created_at DESC LIMIT ?`
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		var action models.ActionLog
		err := rows.Scan(&action.ID, &action.UserID, &action.StackID, &action.Action, &action.Detail, &action.CreatedAt)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (d *DB) GetSetting(key string) (string, error) {
	var value string
	query := `SELECT value FROM settings WHERE key = ?`
	err := d.db.QueryRow(query, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (d *DB) SetSetting(key, value string) error {
	query := `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`
	_, err := d.db.Exec(query, key, value)
	return err
}
