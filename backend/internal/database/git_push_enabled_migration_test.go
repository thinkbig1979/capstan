package database

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestGitPushEnabledMigration pins migration 25 (agent-os-k81p.1): a directory
// row written before the column existed survives and reads as push OFF.
//
// The pre-migration arm makes it discriminating: the column must be ABSENT at
// v24, or the post-migration assertions would hold with migration 25 deleted.
func TestGitPushEnabledMigration_ExistingRowsReadFalse(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 24)
	cols := func() string {
		rows := dumpRows(t, db, `SELECT group_concat(name, ',') FROM pragma_table_info('directories')`)
		require.Len(t, rows, 1)
		return rows[0][0]
	}
	require.NotContains(t, cols(), "git_push_enabled", "the column must not exist before migration 25")

	_, err = db.db.Exec(`INSERT INTO directories (path, name, is_git_repo, scanned_at) VALUES ('/stacks/pre-25', 'pre-25', 1, '2026-09-01T00:00:00Z')`)
	require.NoError(t, err)

	require.NoError(t, RunMigrations(db))
	assert.Contains(t, cols(), "git_push_enabled")

	dir, err := db.GetDirectory("/stacks/pre-25")
	require.NoError(t, err)
	assert.False(t, dir.GitPushEnabled, "an install that predates the switch stays read-only")

	listed, err := db.ListDirectories()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.False(t, listed[0].GitPushEnabled)
}

func TestGitPushEnabledMigration_NewRowsDefaultFalse(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/stacks/new", Name: "new", ScannedAt: time.Now()}))
	dir, err := db.GetDirectory("/stacks/new")
	require.NoError(t, err)
	assert.False(t, dir.GitPushEnabled)
}

// TestGitPushEnabledSurvivesRescan: a scan that still finds the directory
// upserts its row, and a scan is not a decision to arm a push. The switch is
// set TRUE first; a test that leaves the default false is green even when the
// upsert resets the column.
func TestGitPushEnabledSurvivesRescan(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	dir := models.Directory{Path: "/stacks/scan", Name: "scan", ScannedAt: time.Now()}
	require.NoError(t, db.UpsertDirectory(dir))
	require.NoError(t, db.SetDirectoryPushEnabled(dir.Path, true))

	dir.Name = "scan-renamed"
	dir.GitRemote = "git@example.com:org/repo.git"
	require.NoError(t, db.UpsertDirectory(dir))
	created, err := db.InsertDirectoryIfAbsent(dir)
	require.NoError(t, err)
	require.False(t, created, "the row exists, so this call must not have created one")

	got, err := db.GetDirectory(dir.Path)
	require.NoError(t, err)
	require.Equal(t, "scan-renamed", got.Name, "the upsert ran and refreshed scan metadata")
	assert.True(t, got.GitPushEnabled, "a rescan must not reset the push switch")
}

func TestSetDirectoryPushEnabled_RoundTripAndNotFound(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/stacks/a", Name: "a", ScannedAt: time.Now()}))
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/stacks/b", Name: "b", ScannedAt: time.Now()}))

	require.NoError(t, db.SetDirectoryPushEnabled("/stacks/a", true))
	a, err := db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	b, err := db.GetDirectory("/stacks/b")
	require.NoError(t, err)
	assert.True(t, a.GitPushEnabled)
	assert.False(t, b.GitPushEnabled, "the switch is per directory")

	require.NoError(t, db.SetDirectoryPushEnabled("/stacks/a", false))
	a, err = db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	assert.False(t, a.GitPushEnabled)

	err = db.SetDirectoryPushEnabled("/stacks/unregistered", true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errdefs.ErrNotFound), "an unknown path is not a silent no-op, got %v", err)
}
