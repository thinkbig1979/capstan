package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-95dk, real restic: pins what the fake runners assume. `restic tag`
// rewrites the snapshot and records "original" (TagPartial finds the new id),
// and `forget --no-lock --dry-run --json` lists keep and remove with the tags
// and times ApplyRetentionAfterPartial reads. Two partial nights after one
// complete night: the complete snapshot survives both, the older partial goes.
func TestBackup95dk_RealRestic_PartialNightsKeepTheCompleteSnapshot(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed; the real-retention arm cannot run")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so no partial backup can be made")
	}
	dir := t.TempDir()
	stack := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(stack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stack, "a.txt"), []byte("a"), 0o600))
	locked := filepath.Join(stack, "locked.db")
	require.NoError(t, os.WriteFile(locked, []byte("x"), 0o600))
	t.Setenv("RESTIC_CACHE_DIR", filepath.Join(dir, "restic-cache"))

	cfg := testBackupConfig()
	cfg.ResticRepository = filepath.Join(dir, "repo")
	cfg.ResticPassword = "dk95-test-password"
	cfg.KeepDaily, cfg.KeepWeekly, cfg.KeepMonthly, cfg.KeepYearly = 1, 0, 0, 0
	cfg.AutoPrune = false
	m := NewResticManager(cfg, nil)
	ctx := context.Background()
	require.NoError(t, m.EnsureRepository(ctx))

	backup := func() *ResticBackupSummary {
		out, closeOut := drainStream()
		summary, err := m.Backup(ctx, stack, []string{"dk95"}, out)
		closeOut()
		if err != nil {
			require.True(t, isPartialBackup(err, summary), "only a partial backup may fail here: %v", err)
		}
		return summary
	}
	partialNight := func() (string, string) {
		require.NoError(t, os.Chmod(locked, 0o000))
		defer func() { require.NoError(t, os.Chmod(locked, 0o600)) }()
		summary := backup()
		require.Positive(t, summary.UnreadableCount, "fixture is unarmed: the backup read everything")
		out, closeOut := drainStream()
		newID, err := m.TagPartial(ctx, "dk95", summary.SnapshotID, out)
		closeOut()
		require.NoError(t, err)
		require.NotEqual(t, summary.SnapshotID, newID, "restic tag rewrites the id")
		out, closeOut = drainStream()
		kept, err := m.ApplyRetentionAfterPartial(ctx, "dk95", newID, nil, out)
		closeOut()
		require.NoError(t, err)
		return newID, kept
	}

	complete := backup().SnapshotID
	first, kept1 := partialNight()
	assert.Equal(t, shortSnapshotID(complete), kept1, "keep-daily 1 would have forgotten the complete snapshot")
	second, kept2 := partialNight()
	assert.Equal(t, shortSnapshotID(complete), kept2)

	snaps, err := m.ListSnapshots(ctx, "dk95", 0)
	require.NoError(t, err)
	var ids []string
	for _, s := range snaps {
		ids = append(ids, s.ID)
		if s.ID == second {
			assert.Contains(t, s.Tags, PartialSnapshotTag)
		}
	}
	assert.Contains(t, ids, complete, "the complete snapshot survives")
	assert.Contains(t, ids, second, "the newest snapshot is the policy's to keep")
	assert.False(t, slices.Contains(ids, first), "the older partial snapshot is forgotten: %v", ids)
}
