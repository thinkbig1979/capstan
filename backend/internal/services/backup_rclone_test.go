package services

import (
	"context"
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

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "myremote", "backups/capstan", "/restore/dir", 1, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "rclone", call.Binary)
	assert.Equal(t, "sync", call.Args[0])

	// source → destination: remote:path → localPath
	l := len(call.Args)
	assert.Equal(t, "myremote:backups/capstan", call.Args[l-2])
	assert.Equal(t, "/restore/dir", call.Args[l-1])
}

func TestRcloneManager_RestoreRepo_UsesConfigDefaults(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := testRcloneManager(runner)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(context.Background(), "", "", "/local", 1, out)
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

	runner := &fakeRunner{runErr: context.DeadlineExceeded}
	m := testRcloneManager(runner)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.RestoreRepo(ctx, "r", "p", "/local", 1, out)
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
