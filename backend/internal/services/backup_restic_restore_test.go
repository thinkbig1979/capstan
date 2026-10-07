package services

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// drainStream returns a channel that discards everything sent on it, and a
// func that closes it and waits for the drain to finish.
func drainStream() (chan StreamLine, func()) {
	out := make(chan StreamLine, 256)
	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()
	return out, func() { close(out); <-done }
}

// TestResticManager_Restore_RealRestic_MatchesSnapshot is agent-os-z91e.8's
// behavioural arm: a file created after the snapshot must not survive the
// restore, while a cache directory Backup() never covered keeps its content.
func TestResticManager_Restore_RealRestic_MatchesSnapshot(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed; the real-restore arm cannot run")
	}

	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	stack := filepath.Join(dir, "stack")
	cacheDir := filepath.Join(stack, "cache")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stack, "a.txt"), []byte("snapshot"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "CACHEDIR.TAG"), []byte(cacheDirTagSignature+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "before.bin"), []byte("cache"), 0o600))
	// A cache directory whose name is full of glob metacharacters: an unescaped
	// exclude would match some other name and leave this one to --delete.
	oddCache := filepath.Join(stack, `c[1]*?\x`)
	writeCacheDir(t, oddCache, cacheDirTagSignature)
	t.Setenv("RESTIC_CACHE_DIR", filepath.Join(dir, "restic-cache"))

	cfg := testBackupConfig()
	cfg.ResticRepository = repo
	cfg.ResticPassword = "z91e8-test-password"
	m := NewResticManager(cfg, nil)
	ctx := context.Background()
	require.NoError(t, m.EnsureRepository(ctx))

	out, closeOut := drainStream()
	summary, err := m.Backup(ctx, stack, []string{"z91e8"}, out)
	closeOut()
	require.NoError(t, err)
	require.NotEmpty(t, summary.SnapshotID)

	// State after the snapshot: a new file, a changed file, new cache content.
	require.NoError(t, os.WriteFile(filepath.Join(stack, "b.txt"), []byte("post-snapshot"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(stack, "a.txt"), []byte("changed"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "after.bin"), []byte("cache"), 0o600))

	excludes, err := restoreProtectedPaths(stack, stack)
	require.NoError(t, err)
	out, closeOut = drainStream()
	err = m.Restore(ctx, summary.SnapshotID, stack, stack, excludes, out)
	closeOut()
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(stack, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "snapshot", string(got), "a file in the snapshot is restored to its snapshot content")
	require.NoFileExists(t, filepath.Join(stack, "b.txt"),
		"a file created after the snapshot must not survive the restore")
	// The cache directory's content was never in the snapshot (--exclude-caches),
	// so --delete must leave it alone: before.bin predates the snapshot.
	require.FileExists(t, filepath.Join(cacheDir, "before.bin"), "pre-snapshot cache content must survive")
	require.FileExists(t, filepath.Join(cacheDir, "after.bin"), "post-snapshot cache content must survive")
	require.FileExists(t, filepath.Join(oddCache, "data.bin"), "an oddly named cache directory must survive too")
}

// writeCacheDir creates dir with a CACHEDIR.TAG holding tag and one data file.
func writeCacheDir(t *testing.T, dir, tag string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CACHEDIR.TAG"), []byte(tag), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.bin"), []byte("x"), 0o600))
}

// foreignDevice reports every entry named in foreign as sitting on device 2
// and everything else on device 1, standing in for a nested mount without root.
func foreignDevice(foreign ...string) func(fs.FileInfo) (uint64, bool) {
	return func(fi fs.FileInfo) (uint64, bool) {
		for _, name := range foreign {
			if fi.Name() == name {
				return 2, true
			}
		}
		return 1, true
	}
}

func TestScanRestoreProtected_ListsWhatTheBackupSkipped(t *testing.T) {
	t.Parallel()

	stack := filepath.Join(t.TempDir(), "stack")
	require.NoError(t, os.MkdirAll(filepath.Join(stack, "plain"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stack, "a.txt"), []byte("a"), 0o600))
	writeCacheDir(t, filepath.Join(stack, "cache"), cacheDirTagSignature+"\n")
	writeCacheDir(t, filepath.Join(stack, "sub", "cache2"), cacheDirTagSignature)
	// Not caches by restic's rule: a wrong signature, and one too short to hold it.
	writeCacheDir(t, filepath.Join(stack, "badtag"), "Signature: 0000000000000000000000000000000\n")
	writeCacheDir(t, filepath.Join(stack, "shorttag"), "Signature:")
	// Stand-ins for mounts: a directory (whose children must not be listed on
	// their own) and a single file.
	require.NoError(t, os.MkdirAll(filepath.Join(stack, "mnt", "inner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stack, "filemount"), []byte("f"), 0o600))

	got, err := scanRestoreProtected(stack, stack, foreignDevice("mnt", "filemount"))
	require.NoError(t, err)
	sort.Strings(got)
	assert.Equal(t, []string{"cache", "filemount", "mnt", filepath.Join("sub", "cache2")}, got)
}

func TestScanRestoreProtected_RelativeToSubdirectoryTarget(t *testing.T) {
	t.Parallel()

	stack := filepath.Join(t.TempDir(), "stack")
	writeCacheDir(t, filepath.Join(stack, "sub", "cache"), cacheDirTagSignature)
	writeCacheDir(t, filepath.Join(stack, "outside"), cacheDirTagSignature)

	got, err := scanRestoreProtected(stack, filepath.Join(stack, "sub"), foreignDevice())
	require.NoError(t, err)
	assert.Equal(t, []string{"cache"}, got, "paths are relative to the restore target, and nothing outside it is scanned")
}

func TestScanRestoreProtected_RefusesATargetTheBackupNeverCovered(t *testing.T) {
	t.Parallel()

	stack := filepath.Join(t.TempDir(), "stack")
	require.NoError(t, os.MkdirAll(filepath.Join(stack, "mnt"), 0o755))
	writeCacheDir(t, filepath.Join(stack, "cache"), cacheDirTagSignature)
	require.NoError(t, os.MkdirAll(filepath.Join(stack, "real"), 0o755))
	require.NoError(t, os.Symlink("real", filepath.Join(stack, "link")))

	for name, target := range map[string]string{
		"another filesystem": filepath.Join(stack, "mnt"),
		"cache directory":    filepath.Join(stack, "cache"),
		"symbolic link":      filepath.Join(stack, "link"),
	} {
		_, err := scanRestoreProtected(stack, target, foreignDevice("mnt"))
		assert.Error(t, err, name)
	}
	// The same instrument passes on a target the backup did cover.
	_, err := scanRestoreProtected(stack, filepath.Join(stack, "real"), foreignDevice("mnt"))
	assert.NoError(t, err)
}

func TestScanRestoreProtected_MissingDirectoriesProtectNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got, err := scanRestoreProtected(filepath.Join(root, "gone"), filepath.Join(root, "gone"), statDevice)
	require.NoError(t, err)
	assert.Empty(t, got)

	stack := filepath.Join(root, "stack")
	require.NoError(t, os.MkdirAll(stack, 0o755))
	got, err = scanRestoreProtected(stack, filepath.Join(stack, "new"), statDevice)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestScanRestoreProtected_UnreadableDirectoryFailsClosed(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0o000 directory, so it cannot stand in for an unreadable one")
	}

	stack := filepath.Join(t.TempDir(), "stack")
	locked := filepath.Join(stack, "locked")
	require.NoError(t, os.MkdirAll(locked, 0o755))
	_, err := scanRestoreProtected(stack, stack, statDevice)
	require.NoError(t, err, "the same tree scans cleanly while the directory is readable")

	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	_, err = scanRestoreProtected(stack, stack, statDevice)
	require.Error(t, err, "a directory the scan cannot read might hold a mount or cache, so the restore is refused")
}

func TestResticManager_Restore_ExcludesAreAnchoredAndEscaped(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)
	out, closeOut := drainStream()
	err := m.Restore(context.Background(), "abc123", "/orig/src", "/orig/src",
		[]string{"mnt", filepath.Join("sub", "cache"), `c[1]*?\x`}, out)
	closeOut()
	require.NoError(t, err)

	args := runner.lastCall().Args
	assert.True(t, argPairContains(args, "--exclude", "/mnt"))
	assert.True(t, argPairContains(args, "--exclude", "/sub/cache"))
	assert.True(t, argPairContains(args, "--exclude", `/c\[1\]\*\?\\x`))
}

// seedStackAt registers stackID with its directory at dir, for restore tests
// that need a real directory on disk.
func seedStackAt(t *testing.T, db interface {
	UpsertDirectory(models.Directory) error
	UpsertStack(models.Stack) error
}, stackID, dir string) {
	t.Helper()
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: dir, Name: stackID, RootDir: filepath.Dir(dir)}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: stackID, Directory: dir, ProjectName: stackID, Status: "running"}))
}

func TestRunRestore_PassesProtectedPathsToRestic(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc := buildSvc(t, db, docker, runner, runner)
	stack := filepath.Join(t.TempDir(), "myapp")
	writeCacheDir(t, filepath.Join(stack, "cache"), cacheDirTagSignature)
	seedStackAt(t, db, "myapp", stack)

	out, closeOut := drainStream()
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "", out)
	closeOut()
	require.NoError(t, err)

	call := runner.lastCall()
	require.Equal(t, "restore", call.Args[0])
	assert.Contains(t, call.Args, "--delete")
	assert.True(t, argPairContains(call.Args, "--exclude", "/cache"),
		"the cache directory found on disk must reach restic as an exclude: %v", call.Args)
}

func TestRunRestore_ScanFailureRefusesBeforeStopping(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0o000 directory, so it cannot stand in for an unreadable one")
	}

	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSON("abc123", "abc123", "myapp")}
	svc := buildSvc(t, db, docker, runner, runner)
	stack := filepath.Join(t.TempDir(), "myapp")
	locked := filepath.Join(stack, "locked")
	require.NoError(t, os.MkdirAll(locked, 0o755))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	seedStackAt(t, db, "myapp", stack)

	out, closeOut := drainStream()
	err := svc.RunRestore(context.Background(), "myapp", "abc123", "", out)
	closeOut()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore refused")
	assert.Equal(t, 0, docker.stopped(), "a refused restore must leave the stack running")
	for _, c := range runner.calls {
		assert.NotEqual(t, "restore", c.Args[0], "restic restore must not run after a failed scan")
	}
}
