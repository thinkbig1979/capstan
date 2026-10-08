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
