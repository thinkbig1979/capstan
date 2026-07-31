package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVacuumInto_ProducesAnOpenableStandaloneCopy pins the primitive the backup
// engine relies on (agent-os-36o).
//
// The point is not that a file appears — it is that the file is a complete,
// self-contained database containing data committed right up to the snapshot.
// capstan.db runs in WAL mode, so a plain file copy can miss recent commits
// that are still in the -wal sidecar. This asserts the copy round-trips through
// a fresh connection and carries the data.
func TestVacuumInto_ProducesAnOpenableStandaloneCopy(t *testing.T) {
	t.Parallel()

	src, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { src.Close() })

	require.NoError(t, src.SetSetting("canary", "vacuum-into-round-trip"))

	// Stage the copy as <dir>/capstan.db so it can be reopened by passing <dir>
	// to NewWithMigrations, which is exactly how a restore puts it back.
	restoreDir := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, os.MkdirAll(restoreDir, 0o700))
	dest := filepath.Join(restoreDir, "capstan.db")
	require.NoError(t, src.VacuumInto(dest))

	info, statErr := os.Stat(dest)
	require.NoError(t, statErr, "the snapshot file must exist")
	assert.Greater(t, info.Size(), int64(0), "the snapshot must not be empty")

	// No sidecars: VACUUM INTO emits a standalone database, which is what makes
	// it safe to hand to restic as a single file.
	for _, sidecar := range []string{dest + "-wal", dest + "-shm"} {
		_, sErr := os.Stat(sidecar)
		assert.True(t, os.IsNotExist(sErr), "%s must not exist alongside the snapshot", sidecar)
	}

	// Reopen the copy independently and confirm the committed row is present.
	restored, err := NewWithMigrations(restoreDir)
	require.NoError(t, err, "the snapshot must be openable as a database in its own right")
	t.Cleanup(func() { restored.Close() })

	got, err := restored.GetSetting("canary")
	require.NoError(t, err)
	assert.Equal(t, "vacuum-into-round-trip", got,
		"data committed before the snapshot must survive into the copy")
}

// TestVacuumInto_RefusesToOverwrite documents that SQLite will not clobber an
// existing destination. Callers must remove a stale file deliberately —
// silently overwriting would destroy the artifact a restore depends on.
func TestVacuumInto_RefusesToOverwrite(t *testing.T) {
	t.Parallel()

	src, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { src.Close() })

	dest := filepath.Join(t.TempDir(), "snapshot.db")
	require.NoError(t, os.WriteFile(dest, []byte("pre-existing"), 0o600))

	assert.Error(t, src.VacuumInto(dest), "VACUUM INTO must refuse an existing destination")
}
