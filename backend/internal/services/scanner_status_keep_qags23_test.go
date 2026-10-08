package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-qags.23: a lifecycle handler stores the verified status after a
// start or stop, and the Docker-down fallback shows that stored value. A
// rescan (the watcher fires one on any file change) used to write "unknown"
// over it through UpsertStack's INSERT OR REPLACE.
func TestScannerService_ScanAll_KeepsTheStoredStatusOfAnExistingStack(t *testing.T) {
	tempDir := t.TempDir()
	writeComposeStack(t, tempDir, "app")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := NewScannerService(&config.Config{StacksDir: tempDir}, db)

	_, err = s.ScanAll()
	require.NoError(t, err)
	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1, "setup: one stack registered")
	id := stacks[0].ID
	assert.Equal(t, "unknown", stacks[0].Status, "a NEW stack still starts as unknown")

	require.NoError(t, db.UpdateStackStatus(id, "running"))

	_, err = s.ScanAll()
	require.NoError(t, err)

	row, err := db.GetStack(id)
	require.NoError(t, err)
	assert.Equal(t, "running", row.Status, "a rescan must not reset the status a lifecycle handler stored")
}
