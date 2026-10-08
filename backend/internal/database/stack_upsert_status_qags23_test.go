package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-qags.23: UpsertStack on an existing id refreshes every column the
// scanner owns and keeps status, which only the lifecycle handlers write. A
// new id takes the status it was given.
func TestUpsertStack_ExistingRowKeepsStatusAndTakesTheOtherColumns(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/app", Name: "app", RootDir: "/srv"}))

	require.NoError(t, db.UpsertStack(models.Stack{ID: "s1", Directory: "/srv/app", ComposeFile: "compose.yaml", ProjectName: "app", Status: "stopped"}))
	first, err := db.GetStack("s1")
	require.NoError(t, err)
	assert.Equal(t, "stopped", first.Status, "a new row takes the status it was given")

	require.NoError(t, db.UpdateStackStatus("s1", "running"))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "s1", Directory: "/srv/app", ComposeFile: "compose.yml", EnvFile: ".env", ProjectName: "renamed",
		Status: "unknown", IsGitRepo: true, GitBranch: "main",
	}))

	got, err := db.GetStack("s1")
	require.NoError(t, err)
	assert.Equal(t, "running", got.Status, "the stored status survives an upsert")
	assert.Equal(t, "compose.yml", got.ComposeFile)
	assert.Equal(t, ".env", got.EnvFile)
	assert.Equal(t, "renamed", got.ProjectName)
	assert.True(t, got.IsGitRepo)
	assert.Equal(t, "main", got.GitBranch)
}

// An upsert's column list can silently lose a column (INSERT OR REPLACE could
// not): every non-status column must take the new value on an existing id.
// Each value below differs from its first-insert value, so dropping any one
// column from UpsertStack's DO UPDATE SET leaves that column on the old value.
func TestUpsertStack_ExistingRowRefreshesEveryNonStatusColumn(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/a", Name: "a", RootDir: "/srv"}))
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/b", Name: "b", RootDir: "/srv"}))

	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "s1", Directory: "/srv/a", ComposeFile: "compose.yaml", EnvFile: ".env", ProjectName: "one",
		Status: "stopped", IsGitRepo: false, GitBranch: "main", GitCommit: "aaa111",
		GitDirty: false, GitAhead: 1, GitBehind: 2,
	}))
	require.NoError(t, db.UpdateStackStatus("s1", "running"))

	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "s1", Directory: "/srv/b", ComposeFile: "compose.yml", EnvFile: ".env.other", ProjectName: "two",
		Status: "unknown", IsGitRepo: true, GitBranch: "dev", GitCommit: "bbb222",
		GitDirty: true, GitAhead: 7, GitBehind: 9,
	}))

	got, err := db.GetStack("s1")
	require.NoError(t, err)
	assert.Equal(t, "running", got.Status, "status keeps the stored value")
	assert.Equal(t, "/srv/b", got.Directory)
	assert.Equal(t, "compose.yml", got.ComposeFile)
	assert.Equal(t, ".env.other", got.EnvFile)
	assert.Equal(t, "two", got.ProjectName)
	assert.True(t, got.IsGitRepo)
	assert.Equal(t, "dev", got.GitBranch)
	assert.Equal(t, "bbb222", got.GitCommit)
	assert.True(t, got.GitDirty)
	assert.Equal(t, 7, got.GitAhead)
	assert.Equal(t, 9, got.GitBehind)
}
