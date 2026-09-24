package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-d1c7: action_log.detail, directories.git_remote/git_branch and
// stacks.env_file/git_branch/git_commit are nullable columns read into plain
// Go strings. One NULL row used to fail rows.Scan and lose the reader's whole
// result. No Capstan writer stores a NULL in any of them (every writer binds a
// Go string, and the migrations that touch them copy verbatim; established by
// a writer sweep, not tested), so each row below is inserted by raw SQL, the
// way a hand-edited or externally written database would hold it.

func TestActionLogReaders_SurviveNullDetail(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.LogAction(models.ActionLog{ID: "a-real", UserID: "u", StackID: "s1",
		Action: "stack.start", Detail: `{"k":"v"}`, CreatedAt: base.Add(time.Hour)}))
	_, err := db.db.Exec(`INSERT INTO action_log (id, user_id, stack_id, action, created_at) VALUES (?, ?, ?, ?, ?)`,
		"a-null", "u", "s1", "stack.stop", base)
	require.NoError(t, err)

	assertEntries := func(t *testing.T, got []models.ActionLog) {
		t.Helper()
		require.Len(t, got, 2)
		assert.Equal(t, "a-real", got[0].ID)
		assert.Equal(t, `{"k":"v"}`, got[0].Detail)
		assert.Equal(t, "a-null", got[1].ID)
		assert.Empty(t, got[1].Detail)
	}

	byStack, err := db.GetActionsByStack("s1", 10)
	require.NoError(t, err)
	assertEntries(t, byStack)

	recent, err := db.GetRecentActions(10)
	require.NoError(t, err)
	assertEntries(t, recent)

	page, total, err := db.ListActionLogsFiltered(10, 0, ActionLogFilter{})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assertEntries(t, page)
}

func TestDirectoryReaders_SurviveNullGitColumns(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/a", Name: "a", IsGitRepo: true,
		GitRemote: "git@example.com:a.git", GitBranch: "main", ScannedAt: time.Now()}))
	_, err := db.db.Exec(`INSERT INTO directories (path, name) VALUES (?, ?)`, "/srv/b", "b")
	require.NoError(t, err)

	dirs, err := db.ListDirectories()
	require.NoError(t, err)
	require.Len(t, dirs, 2)
	assert.Equal(t, "/srv/a", dirs[0].Path)
	assert.Equal(t, "git@example.com:a.git", dirs[0].GitRemote)
	assert.Equal(t, "main", dirs[0].GitBranch)
	assert.Equal(t, "/srv/b", dirs[1].Path)
	assert.Empty(t, dirs[1].GitRemote)
	assert.Empty(t, dirs[1].GitBranch)

	dir, err := db.GetDirectory("/srv/b")
	require.NoError(t, err)
	assert.Equal(t, "b", dir.Name)
	assert.Empty(t, dir.GitRemote)
	assert.Empty(t, dir.GitBranch)
}

func TestStackReaders_SurviveNullTextColumns(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/d", Name: "d", ScannedAt: time.Now()}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-real", Directory: "/srv/d", ComposeFile: "compose.yaml",
		EnvFile: ".env", ProjectName: "alpha", Status: "running", IsGitRepo: true, GitBranch: "main", GitCommit: "abc123"}))
	_, err := db.db.Exec(`INSERT INTO stacks (id, directory, compose_file, project_name) VALUES (?, ?, ?, ?)`,
		"s-null", "/srv/d", "other.yaml", "beta")
	require.NoError(t, err)

	assertStacks := func(t *testing.T, got []models.Stack) {
		t.Helper()
		require.Len(t, got, 2)
		assert.Equal(t, "s-real", got[0].ID)
		assert.Equal(t, ".env", got[0].EnvFile)
		assert.Equal(t, "main", got[0].GitBranch)
		assert.Equal(t, "abc123", got[0].GitCommit)
		assertNullStack(t, &got[1])
	}

	all, err := db.ListStacks()
	require.NoError(t, err)
	assertStacks(t, all)

	byDir, err := db.ListStacksByDirectory("/srv/d")
	require.NoError(t, err)
	assertStacks(t, byDir)

	byID, err := db.GetStack("s-null")
	require.NoError(t, err)
	assertNullStack(t, byID)

	byProject, err := db.GetStackByProjectName("beta")
	require.NoError(t, err)
	assertNullStack(t, byProject)
}

func assertNullStack(t *testing.T, s *models.Stack) {
	t.Helper()
	assert.Equal(t, "s-null", s.ID)
	assert.Empty(t, s.EnvFile)
	assert.Empty(t, s.GitBranch)
	assert.Empty(t, s.GitCommit)
}
