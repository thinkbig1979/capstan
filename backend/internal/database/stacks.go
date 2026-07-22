package database

import (
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
