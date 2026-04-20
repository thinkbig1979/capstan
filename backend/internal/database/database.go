package database

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/docker-manager/backend/internal/models"
)

type DB struct {
	db *sql.DB
}

func New(dataDir string) (*DB, error) {
	var dbPath string
	if dataDir == ":memory:" {
		dbPath = ":memory:"
	} else {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, err
		}
		dbPath = dataDir + "/docker-manager.db"
	}

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

func NewWithMigrations(dataDir string) (*DB, error) {
	db, err := New(dataDir)
	if err != nil {
		return nil, err
	}
	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
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
	          (path, name, is_git_repo, git_remote, git_branch, git_auth_type, git_ssh_key_path, git_https_user, git_https_token, scanned_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, dir.Path, dir.Name, dir.IsGitRepo, dir.GitRemote, dir.GitBranch,
		dir.GitAuthType, dir.GitSSHKeyPath, dir.GitHTTPSUser, dir.GitHTTPSToken, dir.ScannedAt)
	return err
}

func (d *DB) ListDirectories() ([]models.Directory, error) {
	query := `SELECT path, name, is_git_repo, git_remote, git_branch, git_auth_type, git_ssh_key_path, git_https_user, git_https_token, scanned_at
	          FROM directories ORDER BY name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	directories := make([]models.Directory, 0)
	for rows.Next() {
		var dir models.Directory
		err := rows.Scan(&dir.Path, &dir.Name, &dir.IsGitRepo, &dir.GitRemote, &dir.GitBranch,
			&dir.GitAuthType, &dir.GitSSHKeyPath, &dir.GitHTTPSUser, &dir.GitHTTPSToken, &dir.ScannedAt)
		if err != nil {
			return nil, err
		}
		dir.HasHTTPSToken = dir.GitHTTPSToken != ""
		directories = append(directories, dir)
	}
	return directories, nil
}

func (d *DB) GetDirectory(path string) (*models.Directory, error) {
	var dir models.Directory
	query := `SELECT path, name, is_git_repo, git_remote, git_branch, git_auth_type, git_ssh_key_path, git_https_user, git_https_token, scanned_at
	          FROM directories WHERE path = ?`
	err := d.db.QueryRow(query, path).Scan(&dir.Path, &dir.Name, &dir.IsGitRepo, &dir.GitRemote, &dir.GitBranch,
		&dir.GitAuthType, &dir.GitSSHKeyPath, &dir.GitHTTPSUser, &dir.GitHTTPSToken, &dir.ScannedAt)
	if err != nil {
		return nil, err
	}
	dir.HasHTTPSToken = dir.GitHTTPSToken != ""
	return &dir, nil
}

func (d *DB) DeleteDirectory(path string) error {
	query := `DELETE FROM directories WHERE path = ?`
	_, err := d.db.Exec(query, path)
	return err
}

func (d *DB) UpdateDirectoryCredentials(path, authType, sshKeyPath, httpsUser, httpsToken string) error {
	query := `UPDATE directories SET git_auth_type = ?, git_ssh_key_path = ?, git_https_user = ?, git_https_token = ? WHERE path = ?`
	_, err := d.db.Exec(query, authType, sshKeyPath, httpsUser, httpsToken, path)
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

func (d *DB) DeleteOldActionLogs(retentionDays int) error {
	query := `DELETE FROM action_log WHERE created_at < datetime('now', '-' || ? || ' days')`
	_, err := d.db.Exec(query, retentionDays)
	return err
}

func (d *DB) GetCachedUpdates() ([]models.CachedUpdate, error) {
	query := `SELECT id, container_id, container_name, image, image_ref, state,
	          COALESCE(stack_id, ''), COALESCE(project_name, ''), COALESCE(service_name, ''),
	          is_compose, local_digest, remote_digest, scanned_at
	          FROM cached_updates ORDER BY container_name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var updates []models.CachedUpdate
	for rows.Next() {
		var u models.CachedUpdate
		var stackID, projectName, serviceName string
		err := rows.Scan(&u.ID, &u.ContainerID, &u.ContainerName, &u.Image, &u.ImageRef,
			&u.State, &stackID, &projectName, &serviceName,
			&u.IsCompose, &u.LocalDigest, &u.RemoteDigest, &u.ScannedAt)
		if err != nil {
			return nil, err
		}
		if stackID != "" {
			u.StackID = stackID
		}
		if projectName != "" {
			u.ProjectName = projectName
		}
		if serviceName != "" {
			u.ServiceName = serviceName
		}
		updates = append(updates, u)
	}
	return updates, nil
}

func (d *DB) SetCachedUpdates(updates []models.CachedUpdate) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM cached_updates"); err != nil {
		tx.Rollback()
		return fmt.Errorf("clear cached updates: %w", err)
	}

	for _, u := range updates {
		_, err := tx.Exec(`INSERT INTO cached_updates (id, container_id, container_name, image, image_ref, state,
		                  stack_id, project_name, service_name, is_compose, local_digest, remote_digest, scanned_at)
		                  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.ID, u.ContainerID, u.ContainerName, u.Image, u.ImageRef, u.State,
			u.StackID, u.ProjectName, u.ServiceName, u.IsCompose,
			u.LocalDigest, u.RemoteDigest, u.ScannedAt)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("insert cached update: %w", err)
		}
	}

	return tx.Commit()
}

func (d *DB) ClearCachedUpdates() error {
	_, err := d.db.Exec("DELETE FROM cached_updates")
	return err
}

func (d *DB) GetAutoUpdatePolicies() ([]models.AutoUpdatePolicy, error) {
	query := `SELECT id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at
	          FROM auto_update_policies ORDER BY target_type, target_id`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.AutoUpdatePolicy
	for rows.Next() {
		var p models.AutoUpdatePolicy
		err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled,
			&p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, nil
}

func (d *DB) GetAutoUpdatePolicy(targetType, targetID string) (*models.AutoUpdatePolicy, error) {
	var p models.AutoUpdatePolicy
	query := `SELECT id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at
	          FROM auto_update_policies WHERE target_type = ? AND target_id = ?`
	err := d.db.QueryRow(query, targetType, targetID).Scan(&p.ID, &p.TargetType, &p.TargetID,
		&p.Enabled, &p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *DB) UpsertAutoUpdatePolicy(policy *models.AutoUpdatePolicy) error {
	query := `INSERT OR REPLACE INTO auto_update_policies (id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, policy.ID, policy.TargetType, policy.TargetID,
		policy.Enabled, policy.ConsecutiveFailures, policy.Paused,
		policy.CreatedAt, policy.UpdatedAt)
	return err
}

func (d *DB) DeleteAutoUpdatePolicy(targetType, targetID string) error {
	_, err := d.db.Exec("DELETE FROM auto_update_policies WHERE target_type = ? AND target_id = ?", targetType, targetID)
	return err
}

func (d *DB) GetEnabledAutoUpdatePolicies() ([]models.AutoUpdatePolicy, error) {
	query := `SELECT id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at
	          FROM auto_update_policies WHERE enabled = TRUE AND paused = FALSE`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.AutoUpdatePolicy
	for rows.Next() {
		var p models.AutoUpdatePolicy
		err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled,
			&p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, nil
}

func (d *DB) GetUpdateHistory(filters models.UpdateHistoryFilters) ([]models.UpdateHistoryEntry, int, error) {
	var whereClauses []string
	var args []interface{}

	if filters.Status != "" {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, filters.Status)
	}
	if filters.Trigger != "" {
		whereClauses = append(whereClauses, "trigger = ?")
		args = append(args, filters.Trigger)
	}
	if filters.ContainerID != "" {
		whereClauses = append(whereClauses, "container_id = ?")
		args = append(args, filters.ContainerID)
	}
	if filters.StackID != "" {
		whereClauses = append(whereClauses, "stack_id = ?")
		args = append(args, filters.StackID)
	}
	if filters.From != nil {
		whereClauses = append(whereClauses, "started_at >= ?")
		args = append(args, filters.From.Format(time.RFC3339))
	}
	if filters.To != nil {
		whereClauses = append(whereClauses, "started_at <= ?")
		args = append(args, filters.To.Format(time.RFC3339))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM update_history " + whereClause
	if err := d.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = 25
	}
	page := filters.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	query := `SELECT id, container_id, container_name, COALESCE(stack_id, ''), COALESCE(stack_name, ''),
	          image, old_digest, new_digest, old_image_ref, new_image_ref,
	          status, trigger, started_at, completed_at, duration_ms, error_message
	          FROM update_history ` + whereClause + ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(args, limit, offset)

	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []models.UpdateHistoryEntry
	for rows.Next() {
		var e models.UpdateHistoryEntry
		var stackID, stackName sql.NullString
		var oldDigest, newDigest, oldImageRef, newImageRef sql.NullString
		var completedAt sql.NullString
		var durationMs sql.NullInt64
		var errorMsg sql.NullString

		err := rows.Scan(&e.ID, &e.ContainerID, &e.ContainerName, &stackID, &stackName,
			&e.Image, &oldDigest, &newDigest, &oldImageRef, &newImageRef,
			&e.Status, &e.Trigger, &e.StartedAt, &completedAt, &durationMs, &errorMsg)
		if err != nil {
			return nil, 0, err
		}

		if stackID.Valid && stackID.String != "" {
			e.StackID = &stackID.String
		}
		if stackName.Valid && stackName.String != "" {
			e.StackName = &stackName.String
		}
		if oldDigest.Valid {
			e.OldDigest = &oldDigest.String
		}
		if newDigest.Valid {
			e.NewDigest = &newDigest.String
		}
		if oldImageRef.Valid {
			e.OldImageRef = &oldImageRef.String
		}
		if newImageRef.Valid {
			e.NewImageRef = &newImageRef.String
		}
		if completedAt.Valid {
			e.CompletedAt = &completedAt.String
		}
		if durationMs.Valid {
			e.DurationMs = &durationMs.Int64
		}
		if errorMsg.Valid {
			e.ErrorMessage = &errorMsg.String
		}

		entries = append(entries, e)
	}
	return entries, total, nil
}

func (d *DB) InsertUpdateHistory(entry *models.UpdateHistoryEntry) error {
	query := `INSERT INTO update_history (id, container_id, container_name, stack_id, stack_name,
	          image, old_digest, new_digest, old_image_ref, new_image_ref,
	          status, trigger, started_at, completed_at, duration_ms, error_message)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var stackID, stackName interface{}
	if entry.StackID != nil {
		stackID = *entry.StackID
	}
	if entry.StackName != nil {
		stackName = *entry.StackName
	}

	_, err := d.db.Exec(query, entry.ID, entry.ContainerID, entry.ContainerName,
		stackID, stackName, entry.Image,
		entry.OldDigest, entry.NewDigest, entry.OldImageRef, entry.NewImageRef,
		entry.Status, entry.Trigger, entry.StartedAt,
		entry.CompletedAt, entry.DurationMs, entry.ErrorMessage)
	return err
}

func (d *DB) UpdateUpdateHistory(id string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	var setClauses []string
	var args []interface{}
	for key, val := range updates {
		setClauses = append(setClauses, key+" = ?")
		args = append(args, val)
	}
	args = append(args, id)

	query := "UPDATE update_history SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	_, err := d.db.Exec(query, args...)
	return err
}

func (d *DB) DeleteUpdateHistoryOlderThan(before time.Time) (int, error) {
	result, err := d.db.Exec("DELETE FROM update_history WHERE completed_at < ?", before.Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

func (d *DB) GetUpdateStats() (enabledContainers int, last7Days int, last30Days int, err error) {
	err = d.db.QueryRow("SELECT COUNT(*) FROM auto_update_policies WHERE enabled = TRUE").Scan(&enabledContainers)
	if err != nil {
		return
	}

	err = d.db.QueryRow("SELECT COUNT(*) FROM update_history WHERE status = 'success' AND started_at >= datetime('now', '-7 days')").Scan(&last7Days)
	if err != nil {
		return
	}

	err = d.db.QueryRow("SELECT COUNT(*) FROM update_history WHERE status = 'success' AND started_at >= datetime('now', '-30 days')").Scan(&last30Days)
	if err != nil {
		return
	}

	return
}
