package database

import (
	"fmt"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// UpsertStack inserts a stack row, or refreshes an existing one in place. On an
// existing id it rewrites every column except status: the status is written
// only by the lifecycle handlers after a verified start or stop
// (UpdateStackStatus), and the Docker-down fallback shows that stored value, so
// a rescan that rebuilt the row as "unknown" would erase it (agent-os-qags.23).
// A new id takes the status passed in. INSERT OR REPLACE would have replaced
// the whole row; the only UNIQUE key is id, so the other columns behave the same.
// The row-value SET keeps the text "project_name =" out of this file:
// scripts/check-project-name-lookup.sh reads a SET as a query filter.
func (d *DB) UpsertStack(stack models.Stack) error {
	query := `INSERT INTO stacks
	          (id, directory, compose_file, env_file, project_name, status,
	           is_git_repo, git_branch, git_commit, git_dirty, git_ahead, git_behind)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	           (directory, compose_file, env_file, project_name, is_git_repo,
	            git_branch, git_commit, git_dirty, git_ahead, git_behind) =
	           (excluded.directory, excluded.compose_file, excluded.env_file,
	            excluded.project_name, excluded.is_git_repo, excluded.git_branch,
	            excluded.git_commit, excluded.git_dirty, excluded.git_ahead,
	            excluded.git_behind)`
	_, err := d.db.Exec(query, stack.ID, stack.Directory, stack.ComposeFile, stack.EnvFile,
		stack.ProjectName, stack.Status, stack.IsGitRepo, stack.GitBranch, stack.GitCommit,
		stack.GitDirty, stack.GitAhead, stack.GitBehind)
	return err
}

// emptyStack is what every stack reader scans into. There is no containers
// column, so a plain zero value would leave Containers nil, and a handler that
// skips applyLiveStatus (the Docker-outage path) would send "containers":null
// (agent-os-e5pr).
func emptyStack() models.Stack {
	return models.Stack{Containers: []models.Container{}}
}

// stackColumns is the one column list every full-row stacks reader selects,
// in scanStack's target order. Four readers used to carry their own copies; a
// column moved in one copy put values in the wrong fields with no error, since
// neighbouring columns share a type (agent-os-rh7m, as agent-os-13xd did for
// backup_runs).
//
// env_file, git_branch and git_commit are nullable; the COALESCEs keep a NULL
// written outside Capstan from failing rows.Scan and losing the whole list
// (agent-os-d1c7). No Capstan writer stores one.
const stackColumns = `id, directory, compose_file, COALESCE(env_file, ''), project_name, status, ` +
	`is_git_repo, COALESCE(git_branch, ''), COALESCE(git_commit, ''), git_dirty, git_ahead, git_behind`

// scanStack reads one row selected with stackColumns, from *sql.Row or
// *sql.Rows.
func scanStack(row interface{ Scan(dest ...any) error }) (models.Stack, error) {
	stack := emptyStack()
	err := row.Scan(&stack.ID, &stack.Directory, &stack.ComposeFile, &stack.EnvFile,
		&stack.ProjectName, &stack.Status, &stack.IsGitRepo, &stack.GitBranch,
		&stack.GitCommit, &stack.GitDirty, &stack.GitAhead, &stack.GitBehind)
	return stack, err
}

func (d *DB) ListStacks() ([]models.Stack, error) {
	query := `SELECT ` + stackColumns + ` FROM stacks ORDER BY project_name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stacks := make([]models.Stack, 0)
	for rows.Next() {
		stack, err := scanStack(rows)
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
	query := `SELECT ` + stackColumns + ` FROM stacks WHERE id = ?`
	stack, err := scanStack(d.db.QueryRow(query, id))
	if err != nil {
		return nil, notFound(err, "stack", id)
	}
	return &stack, nil
}

func (d *DB) ListStacksByDirectory(path string) ([]models.Stack, error) {
	query := `SELECT ` + stackColumns + ` FROM stacks WHERE directory = ? ORDER BY project_name`
	rows, err := d.db.Query(query, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stacks := make([]models.Stack, 0)
	for rows.Next() {
		stack, err := scanStack(rows)
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

// SetStackEnvFileIfUnset records envFile on the stack row, but only when the
// row has none yet. It touches that one column, so a scan that rewrote the rest
// of the row meanwhile keeps its values, and a row pruned meanwhile stays gone
// (0 rows, set false), where a whole-row UpsertStack would write it back
// (agent-os-z91e.23).
func (d *DB) SetStackEnvFileIfUnset(id, envFile string) (set bool, err error) {
	res, err := d.db.Exec(`UPDATE stacks SET env_file = ? WHERE id = ? AND COALESCE(env_file, '') = ''`, envFile, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *DB) UpdateStackStatus(id, status string) error {
	query := `UPDATE stacks SET status = ? WHERE id = ?`
	_, err := d.db.Exec(query, status, id)
	return err
}

// AmbiguousProjectNameError is what GetStackByProjectName returns when more
// than one stack carries the compose project name. Nothing stops that: the
// column has no UNIQUE, and two directories resolve to one name whenever their
// compose files share a top-level `name:` (agent-os-z91e.19, owner decision
// D25: both stay listed, every lookup by project name refuses). Stacks holds
// every match, ordered by id, so a caller can name them all.
type AmbiguousProjectNameError struct {
	ProjectName string
	Stacks      []models.Stack
}

func (e *AmbiguousProjectNameError) Error() string {
	names := make([]string, 0, len(e.Stacks))
	for _, s := range e.Stacks {
		names = append(names, fmt.Sprintf("%s (%s)", s.ID, s.Directory))
	}
	return fmt.Sprintf("compose project name %q is shared by %d stacks: %s",
		e.ProjectName, len(e.Stacks), strings.Join(names, ", "))
}

func (e *AmbiguousProjectNameError) Is(target error) bool { return target == errdefs.ErrAmbiguous }

func (d *DB) GetStackByProjectName(projectName string) (*models.Stack, error) {
	// Every match, not QueryRow's first: a second row is the ambiguity this
	// lookup must report rather than resolve (agent-os-z91e.19).
	query := `SELECT ` + stackColumns + ` FROM stacks WHERE project_name = ? ORDER BY id`
	rows, err := d.db.Query(query, projectName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []models.Stack
	for rows.Next() {
		stack, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, stack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading stacks for compose project %q: %w", projectName, err)
	}
	switch len(matches) {
	case 0:
		return nil, &errdefs.NotFoundError{Kind: "stack", Key: projectName}
	case 1:
		return &matches[0], nil
	}
	return nil, &AmbiguousProjectNameError{ProjectName: projectName, Stacks: matches}
}
