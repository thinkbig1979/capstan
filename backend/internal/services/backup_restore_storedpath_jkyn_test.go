package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-jkyn: a restore reads the snapshot's own stored path, not the
// stack's current directory, so a snapshot taken before a stacks-dir move
// restores into the current directory; and a snapshot with no usable path
// is refused before the stack is stopped. ---

// jkynSetup seeds a stack at a real current directory and a snapshot whose
// stored paths are paths.
func jkynSetup(t *testing.T, paths string) (*BackupService, *fakeRunner, *fakeDocker, string) {
	t.Helper()
	db := newBackupTestDB(t)
	stack := filepath.Join(t.TempDir(), "new-root", "myapp")
	require.NoError(t, os.MkdirAll(stack, 0o755))
	seedStackAt(t, db, "myapp", stack)
	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: []byte(fmt.Sprintf(
		`[{"id":"abc12345","short_id":"abc12345","time":"2026-11-01T00:00:00Z","hostname":"h","tags":["myapp"],"paths":%s}]`, paths))}
	return buildSvc(t, db, docker, runner, runner), runner, docker, stack
}

// The bead's case: taken under /old-root, restored after the move.
func TestJkyn_SnapshotFromAnotherStacksDirRestoresIntoCurrentDir(t *testing.T) {
	t.Parallel()
	svc, runner, docker, stack := jkynSetup(t, `["/old-root/myapp"]`)

	out, closeOut := drainStream()
	err := svc.RunRestore(context.Background(), "myapp", "abc12345", out)
	closeOut()
	require.NoError(t, err)

	args := restoreCall(t, runner)
	assert.Equal(t, "abc12345:/old-root/myapp", args[1], "the ref names the path the snapshot holds: %v", args)
	assert.True(t, argPairContains(args, "--target", stack), "the target is the current directory: %v", args)
	assert.Equal(t, 1, docker.stopped())
}

// Control: an unmoved stack restores exactly as before.
func TestJkyn_UnmovedStackRefUsesItsOwnDir(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	stack := filepath.Join(t.TempDir(), "myapp")
	require.NoError(t, os.MkdirAll(stack, 0o755))
	seedStackAt(t, db, "myapp", stack)
	runner := &fakeRunner{outputData: snapshotJSONAt("abc12345", "myapp", stack)}
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)

	out, closeOut := drainStream()
	require.NoError(t, svc.RunRestore(context.Background(), "myapp", "abc12345", out))
	closeOut()
	assert.Equal(t, "abc12345:"+stack, restoreCall(t, runner)[1])
}

// A snapshot that names no single absolute path cannot be restored by
// subfolder; it is refused while the stack is still running.
func TestJkyn_SnapshotWithNoUsablePathRefusedBeforeStop(t *testing.T) {
	t.Parallel()
	for name, paths := range map[string]string{
		"no paths":      `[]`,
		"two paths":     `["/a/myapp", "/b/myapp"]`,
		"empty path":    `[""]`,
		"relative path": `["myapp"]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, runner, docker, _ := jkynSetup(t, paths)
			out, closeOut := drainStream()
			err := svc.RunRestore(context.Background(), "myapp", "abc12345", out)
			closeOut()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "path")
			assert.Equal(t, 0, docker.stopped(), "no stop call for a refused restore")
			for _, c := range runner.calls {
				assert.NotEqual(t, "restore", c.Args[0])
			}
		})
	}
}

// The real restic, two-sided: after the stack directory moves, restoring by
// the snapshot's stored path fills the new directory, and restoring by the
// current path (the old behaviour) fails.
func TestJkyn_RealRestic_RestoresByStoredPathAfterMove(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed; the real-restore arm cannot run")
	}
	dir := t.TempDir()
	oldDir := filepath.Join(dir, "old-root", "app")
	newDir := filepath.Join(dir, "new-root", "app")
	require.NoError(t, os.MkdirAll(oldDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "a.txt"), []byte("snapshot"), 0o600))
	t.Setenv("RESTIC_CACHE_DIR", filepath.Join(dir, "restic-cache"))

	cfg := testBackupConfig()
	cfg.ResticRepository = filepath.Join(dir, "repo")
	cfg.ResticPassword = "jkyn-test-password"
	m := NewResticManager(cfg, nil)
	ctx := context.Background()
	require.NoError(t, m.EnsureRepository(ctx))
	out, closeOut := drainStream()
	summary, err := m.Backup(ctx, oldDir, []string{"jkyn"}, out)
	closeOut()
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Dir(newDir), 0o755))
	require.NoError(t, os.Rename(oldDir, newDir))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "a.txt"), []byte("changed"), 0o600))

	out, closeOut = drainStream()
	err = m.Restore(ctx, summary.SnapshotID, newDir, newDir, nil, true, out)
	closeOut()
	require.Error(t, err, "control: the current path is not in the snapshot")

	out, closeOut = drainStream()
	err = m.Restore(ctx, summary.SnapshotID, oldDir, newDir, nil, true, out)
	closeOut()
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(newDir, "a.txt")) //nolint:gosec // G304: newDir is under this test's t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "snapshot", string(got))
	_, err = os.Stat(filepath.Join(newDir, "old-root"))
	assert.True(t, os.IsNotExist(err), "contents land in the target, not nested under the old path")
}
