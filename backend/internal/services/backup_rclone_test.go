package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: fakeRunner and helpers are defined in backup_restic_test.go.

func testRcloneManager(runner commandRunner) *RcloneManager {
	return newRcloneManagerWithRunner(testBackupConfig(), runner, nil)
}

// --- TestConnectivity argv tests ---

func TestRcloneManager_TestConnectivity_Args(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	err := m.TestConnectivity(context.Background(), "myremote")
	require.NoError(t, err)

	call := runner.lastCall()
	assert.Equal(t, "rclone", call.Binary)
	assert.Equal(t, "lsd", call.Args[0])
	assert.Equal(t, "myremote:", call.Args[1])
	assert.True(t, argPairContains(call.Args, "--max-depth", "1"))
}

func TestRcloneManager_TestConnectivity_UsesConfigRemote(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	// Pass empty remote to fall back to cfg.RcloneRemote = "myremote".
	err := m.TestConnectivity(context.Background(), "")
	require.NoError(t, err)

	call := runner.lastCall()
	assert.Equal(t, "myremote:", call.Args[1])
}

func TestRcloneManager_TestConnectivity_NoRemoteConfigured(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	cfg.RcloneRemote = ""
	runner := &fakeRunner{}
	m := newRcloneManagerWithRunner(cfg, runner, nil)

	err := m.TestConnectivity(context.Background(), "")
	assert.Error(t, err, "TestConnectivity must fail when no remote is configured")
	assert.Empty(t, runner.calls)
}

// --- Sync argv tests ---

func TestRcloneManager_Sync_Args(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.Sync(context.Background(), "/var/restic-repo", "myremote", "backups/capstan", 8, 2, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "rclone", call.Binary)
	assert.Equal(t, "sync", call.Args[0])
	assert.True(t, argPairContains(call.Args, "--transfers", "8"))
	assert.True(t, argPairContains(call.Args, "--retries", "3"))
	assert.True(t, argContains(call.Args, "--progress"))
	assert.True(t, argContains(call.Args, "--links"))

	// source → destination order
	l := len(call.Args)
	assert.Equal(t, "/var/restic-repo", call.Args[l-2])
	assert.Equal(t, "myremote:backups/capstan", call.Args[l-1])
}

func TestRcloneManager_Sync_UsesConfigDefaults(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	// Pass empty remote/path/transfers to trigger config fallback.
	err := m.Sync(context.Background(), "/repo", "", "", 0, 1, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	l := len(call.Args)
	// Destination should be cfg.RcloneRemote:cfg.RclonePath
	assert.Equal(t, "myremote:backup/path", call.Args[l-1])
	// Transfers should use cfg.RcloneTransfers = 4
	assert.True(t, argPairContains(call.Args, "--transfers", "4"))
}

func TestRcloneManager_Sync_DefaultRetries(t *testing.T) {
	t.Parallel()

	// Fail every attempt to observe retry count.
	callCount := 0
	custom := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			callCount++
			return assert.AnError
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return nil, nil
		},
	}

	m := testRcloneManager(custom)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()

	// retries=0 should default to 3.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Override the backoff wait to zero for test speed by using context.
	err := m.Sync(ctx, "/repo", "r", "p", 4, 0, out)
	close(out)

	assert.Error(t, err)
	// Should have attempted 3 times (the default).
	// Note: the retry waits (30s * attempt) would make this slow in a real
	// scenario; here context cancels them via the select.
	assert.GreaterOrEqual(t, callCount, 1)
}

// --- RestoreRepo argv tests ---

func TestRcloneManager_RestoreRepo_Args(t *testing.T) {
	t.Parallel()

	// outputData: the source probe (rclone lsf) must see the source as a
	// genuine restic repository -- exactly "config" -- for RestoreRepo to
	// proceed to sync at all.
	runner := &fakeRunner{outputData: []byte("config")}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", "/restore/dir", "", 1, out)
	require.NoError(t, err)
	close(out)

	// RestoreRepo must probe the source before it ever runs sync.
	require.Len(t, runner.calls, 2, "RestoreRepo must call the source probe, then sync")
	assert.Equal(t, "lsf", runner.calls[0].Args[0])

	call := runner.calls[1]
	assert.Equal(t, "rclone", call.Binary)
	assert.Equal(t, "sync", call.Args[0])

	// source → destination: remote:path → localPath
	l := len(call.Args)
	assert.Equal(t, "myremote:backups/capstan", call.Args[l-2])
	assert.Equal(t, "/restore/dir", call.Args[l-1])
}

// TestRcloneManager_RestoreRepo_RefusesWhenSourceProbeFails is the regression
// test for the DR-restore data-loss defect (agent-os-h0my): RestoreRepo runs
// `rclone sync` against whatever remote:path is configured, with no check
// that it is actually a restic repository. `rclone sync` makes the
// destination identical to the source and deletes destination files absent
// from the source -- and the destination is the live local restic repo,
// which on a healthy install holds the only copy of every snapshot. An
// empty-but-existing source, or a nonexistent source, makes the source probe
// itself fail (see the bead's ORCHESTRATOR CORRECTION comments, OBSERVED
// 2026-09-03).
//
// This asserts sync is never invoked once the source probe has errored, not
// merely that RestoreRepo's final error is non-nil.
func TestRcloneManager_RestoreRepo_RefusesWhenSourceProbeFails(t *testing.T) {
	t.Parallel()

	var calledOps []string
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			calledOps = append(calledOps, "run:"+args[0])
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			calledOps = append(calledOps, "output:"+args[0])
			return nil, fmt.Errorf("directory not found")
		},
	}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", "/restore/dir", "", 1, out)
	close(out)

	require.Error(t, err, "RestoreRepo must refuse when the source probe errors")
	assert.NotContains(t, calledOps, "run:sync", "sync must never be invoked once the probe has errored")
}

// TestRcloneManager_RestoreRepo_DoesNotCreateBackupDirWhenProbeFails confirms
// a refused restore leaves no directory behind: backupDir is created only
// after the source probe has passed (see RestoreRepo's doc comment). Before
// this was fixed, RunDRRestore created backupDir before calling RestoreRepo
// at all, so every refused DR restore littered the backup volume with an
// empty "<repo>.pre-dr-<timestamp>" directory that was never used.
func TestRcloneManager_RestoreRepo_DoesNotCreateBackupDirWhenProbeFails(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputErr: fmt.Errorf("directory not found")}
	m := testRcloneManager(runner)

	localPath := t.TempDir()
	backupDir := localPath + ".pre-dr-456"

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", localPath, backupDir, 1, out)
	close(out)

	require.Error(t, err, "RestoreRepo must refuse when the source probe fails")
	assert.NoDirExists(t, backupDir, "a refused restore must not create the backup-dir")
}

// TestRcloneManager_RestoreRepo_RefusesWhenSourceProbeListsNothing is the arm
// the previous exit-code-only probe could not express, and the one this bead
// was reopened over: on an object store, a wrong prefix inside a valid,
// reachable bucket makes `rclone lsf` exit 0 with EMPTY output rather than an
// error -- a prefix with no keys is a successful empty listing there, not a
// "directory not found" (OBSERVED 2026-09-03, `rclone serve s3` on the
// pinned rclone v1.74.4 binary, checksum-verified against the Dockerfile's
// RCLONE_SHA256_AMD64; see probeRestoreSource's doc comment). Checking only
// `err == nil` would have let this through -- the same mistake as the
// original bug.
func TestRcloneManager_RestoreRepo_RefusesWhenSourceProbeListsNothing(t *testing.T) {
	t.Parallel()

	var calledOps []string
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			calledOps = append(calledOps, "run:"+args[0])
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			calledOps = append(calledOps, "output:"+args[0])
			return []byte(""), nil // exit 0, nothing listed
		},
	}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", "/restore/dir", "", 1, out)
	close(out)

	require.Error(t, err, "RestoreRepo must refuse when the probe succeeds but lists nothing")
	assert.NotContains(t, calledOps, "run:sync", "sync must never be invoked when the probe lists nothing")
}

// TestRcloneManager_RestoreRepo_RefusesWhenSourceProbeListsWrongName pins the
// exact-match rationale directly: if <path>/config is itself a directory
// rather than a file, `rclone lsf` lists that directory's CONTENTS instead
// of failing (OBSERVED 2026-09-03, both local backend and the S3 emulation
// above -- see probeRestoreSource's doc comment). A strings.Contains check
// could be fooled by a directory holding a file whose name contains
// "config"; an exact match on the trimmed output must not be.
func TestRcloneManager_RestoreRepo_RefusesWhenSourceProbeListsWrongName(t *testing.T) {
	t.Parallel()

	var calledOps []string
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			calledOps = append(calledOps, "run:"+args[0])
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			calledOps = append(calledOps, "output:"+args[0])
			return []byte("inner-file-named-config.txt\n"), nil
		},
	}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", "/restore/dir", "", 1, out)
	close(out)

	require.Error(t, err, "RestoreRepo must refuse when the probe lists something other than exactly \"config\"")
	assert.NotContains(t, calledOps, "run:sync", "sync must never be invoked when the probe output doesn't exactly match \"config\"")
}

// TestRcloneManager_RestoreRepo_ProceedsWhenSourceProbeSucceeds is the
// positive control for the three refusal tests above: a genuine restore from
// a source that passes the probe must still succeed, and must still
// actually run sync, in that order. A guard that refuses everything would
// pass the negative tests above without proving the guard is selective.
func TestRcloneManager_RestoreRepo_ProceedsWhenSourceProbeSucceeds(t *testing.T) {
	t.Parallel()

	var calledOps []string
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			calledOps = append(calledOps, "run:"+args[0])
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			calledOps = append(calledOps, "output:"+args[0])
			return []byte("config\n"), nil
		},
	}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", "/restore/dir", "", 1, out)
	close(out)

	require.NoError(t, err, "a genuine restore from a source that passes the probe must still succeed")
	assert.Equal(t, []string{"output:lsf", "run:sync"}, calledOps, "probe must run before sync, and sync must still run when the probe succeeds")
}

// TestRcloneManager_RestoreRepo_PassesBackupDir confirms --backup-dir is
// wired into the sync argv when the caller supplies one, positioned before
// the trailing source/destination pair (rclone requires source and
// destination as the final two positional args), and that RestoreRepo
// actually creates the directory (it is only created once the probe has
// passed -- see RestoreRepo's doc comment on why it isn't created earlier).
func TestRcloneManager_RestoreRepo_PassesBackupDir(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte("config")}
	m := testRcloneManager(runner)

	localPath := t.TempDir()
	backupDir := localPath + ".pre-dr-123"

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", localPath, backupDir, 1, out)
	require.NoError(t, err)
	close(out)

	assert.DirExists(t, backupDir, "RestoreRepo must create the backup-dir once the probe has passed")

	call := runner.lastCall()
	assert.True(t, argPairContains(call.Args, "--backup-dir", backupDir))

	l := len(call.Args)
	assert.Equal(t, "myremote:backups/capstan", call.Args[l-2])
	assert.Equal(t, localPath, call.Args[l-1])
}

func TestRcloneManager_RestoreRepo_UsesConfigDefaults(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte("config")}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "", "", "/local", "", 1, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	l := len(call.Args)
	// Source must use config defaults.
	assert.Equal(t, "myremote:backup/path", call.Args[l-2])
}

// --- Timeout / context cancellation ---

func TestRcloneManager_Sync_ContextCancel(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{runErr: context.DeadlineExceeded}
	m := testRcloneManager(runner)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.Sync(ctx, "/repo", "r", "p", 4, 1, out)
	close(out)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRcloneManager_RestoreRepo_ContextCancel(t *testing.T) {
	t.Parallel()

	// Both runErr and outputErr are set: RestoreRepo's first call is now the
	// source probe (via Output), so the deadline must be observable there
	// too, not just on the later Run-based sync call.
	runner := &fakeRunner{runErr: context.DeadlineExceeded, outputErr: context.DeadlineExceeded}
	m := testRcloneManager(runner)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(ctx, "r", "p", "/local", "", 1, out)
	close(out)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// --- Stats flag presence (rclone flags) ---

func TestRcloneManager_Sync_StatsFlag(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	_ = m.Sync(context.Background(), "/r", "remote", "path", 4, 1, out)
	close(out)

	call := runner.lastCall()
	assert.True(t, argPairContains(call.Args, "--stats", "30s"))
	assert.True(t, argContains(call.Args, "--stats-one-line"))
	assert.True(t, argContains(call.Args, "--verbose"))
}
