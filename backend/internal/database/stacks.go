package database

import (
	"fmt"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading stacks: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading stacks for directory: %w", err)
	}
	return stacks, nil
}

func (d *DB) DeleteStack(id string) error {
	query := `DELETE FROM stacks WHERE id = ?`
	_, err := d.db.Exec(query, id)
	return err
}

// DeleteDirectoryIfOrphaned removes the directories row for path, but only
// when no stacks row still references it. One directory legitimately holds
// several stacks (one per compose file), so an unconditional delete here
// would risk taking a live sibling's row with it via the ON DELETE CASCADE on
// stacks.directory (migrations.go) — the same data loss agent-os-w8o already
// guards against, reintroduced from the delete side.
//
// The existence check and the delete are one SQL statement, not a
// count-then-delete pair of Go calls, so there is no window for a concurrent
// Create or directory scan to insert a sibling stacks row between them:
// SQLite evaluates the whole statement atomically. A Go-level "count stacks,
// then delete if zero" was considered and rejected for exactly this reason.
//
// deleted reports whether this call removed the row, for callers that want to
// log or assert on it; a row that survives (because a sibling still
// references it, or because it was already gone) is not an error either way.
func (d *DB) DeleteDirectoryIfOrphaned(path string) (deleted bool, err error) {
	query := `DELETE FROM directories WHERE path = ? AND NOT EXISTS (SELECT 1 FROM stacks WHERE directory = ?)`
	res, err := d.db.Exec(query, path, path)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
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
