package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-fmeu, real restic (review S4): a FILE the partial snapshot lacks
// survives `restore --delete` when it is excluded, and is deleted when it is
// not. z91e.8 measured the exclude for directories only.
func TestFmeu_RealRestic_ExcludedUnreadableFileSurvivesDelete(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed; the real-restore arm cannot run")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so no partial backup can be made")
	}
	dir := t.TempDir()
	stack := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(filepath.Join(stack, "data"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stack, "a.txt"), []byte("a"), 0o600))
	locked := filepath.Join(stack, "data", "locked.db")
	require.NoError(t, os.WriteFile(locked, []byte("live data"), 0o600))
	t.Setenv("RESTIC_CACHE_DIR", filepath.Join(dir, "restic-cache"))

	cfg := testBackupConfig()
	cfg.ResticRepository = filepath.Join(dir, "repo")
	cfg.ResticPassword = "fmeu-test-password"
	m := NewResticManager(cfg, nil)
	ctx := context.Background()
	require.NoError(t, m.EnsureRepository(ctx))

	require.NoError(t, os.Chmod(locked, 0o000))
	out, closeOut := drainStream()
	summary, err := m.Backup(ctx, stack, []string{"fmeu"}, out)
	closeOut()
	require.NoError(t, os.Chmod(locked, 0o600))
	require.True(t, isPartialBackup(err, summary), "fixture is unarmed: %v", err)
	require.Equal(t, []string{locked}, summary.Unreadable)

	rels, placed := unreadableRestoreExcludes(stack, summary.Unreadable)
	require.True(t, placed)
	out, closeOut = drainStream()
	err = m.Restore(ctx, summary.SnapshotID, stack, stack, rels, true, out)
	closeOut()
	require.NoError(t, err)
	got, err := os.ReadFile(locked) //nolint:gosec // G304: under this test's t.TempDir()
	require.NoError(t, err, "the excluded file survives --delete")
	assert.Equal(t, "live data", string(got))

	// Control on the same instrument: without the exclude, --delete removes it.
	out, closeOut = drainStream()
	err = m.Restore(ctx, summary.SnapshotID, stack, stack, nil, true, out)
	closeOut()
	require.NoError(t, err)
	_, err = os.Stat(locked)
	assert.True(t, os.IsNotExist(err), "without the exclude --delete removes the file: %v", err)
}
