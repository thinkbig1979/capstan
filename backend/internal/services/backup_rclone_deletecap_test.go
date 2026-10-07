package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The off-site sync mirrors WITH DELETE, so a local repository that lost
// files would otherwise delete them from the only off-site copy too
// (agent-os-z91e.9). These tests pin the bound: a pre-flight count of
// remote-only files refuses with zero deletes when it exceeds the cap, the
// sync itself carries --max-delete as a backstop, and rclone's fatal exit
// (which is how the backstop fires) is never retried, because each attempt
// would delete another N files.

// makeLocalRepo creates n empty files under a fresh directory, spread over
// two sub-directories so keys carry a "/" the way restic's data/xx/ do.
func makeLocalRepo(t *testing.T, n int) (dir string, keys []string) {
	t.Helper()
	dir = t.TempDir()
	for _, sub := range []string{"data/00", "index"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o700))
	}
	for i := range n {
		key := fmt.Sprintf("data/00/pack%04d", i)
		if i%2 == 1 {
			key = fmt.Sprintf("index/idx%04d", i)
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, key), nil, 0o600))
		keys = append(keys, key)
	}
	return dir, keys
}

// remoteListing renders keys as `rclone lsf -R --files-only` prints them,
// plus extra remote-only files.
func remoteListing(keys []string, extra int) []byte {
	lines := slices.Clone(keys)
	for i := range extra {
		lines = append(lines, fmt.Sprintf("data/ff/gone%04d", i))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func drainLines() chan StreamLine {
	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	return out
}

// syncCalls returns the recorded `rclone sync` invocations.
func syncCalls(calls []fakeCall) []fakeCall {
	var got []fakeCall
	for _, c := range calls {
		if len(c.Args) > 0 && c.Args[0] == "sync" {
			got = append(got, c)
		}
	}
	return got
}

func TestRcloneManager_Sync_PassesMaxDeleteCap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		localFiles int
		wantCap    string
	}{
		{"floor of 100 for a small repo", 10, "100"},
		{"20% of the local file count for a large one", 600, "120"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, keys := makeLocalRepo(t, tc.localFiles)
			runner := &fakeRunner{outputData: remoteListing(keys, 0)}
			out := drainLines()
			require.NoError(t, testRcloneManager(runner).Sync(context.Background(), repo, "r", "p", 4, 1, 0, out))
			close(out)

			calls := syncCalls(runner.calls)
			require.Len(t, calls, 1)
			args := calls[0].Args
			assert.True(t, argPairContains(args, "--max-delete", tc.wantCap), "want --max-delete %s in %q", tc.wantCap, args)
			assert.Less(t, slices.Index(args, "--max-delete"), slices.Index(args, "--"), "--max-delete precedes --")
		})
	}
}

func TestRcloneManager_Sync_RefusesWhenRemoteOnlyFilesExceedCap(t *testing.T) {
	t.Parallel()

	repo, keys := makeLocalRepo(t, 10) // cap = floor of 100
	runner := &fakeRunner{outputData: remoteListing(keys, 101)}
	out := drainLines()
	err := testRcloneManager(runner).Sync(context.Background(), repo, "r", "p", 4, 1, 0, out)
	close(out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "101 remote files")
	assert.Contains(t, err.Error(), "cap is 100")
	assert.Empty(t, syncCalls(runner.calls), "a refused sync must not run rclone sync at all (zero deletes)")
}

func TestRcloneManager_Sync_ProceedsAtExactlyTheCap(t *testing.T) {
	t.Parallel()

	repo, keys := makeLocalRepo(t, 10)
	runner := &fakeRunner{outputData: remoteListing(keys, 100)}
	out := drainLines()
	err := testRcloneManager(runner).Sync(context.Background(), repo, "r", "p", 4, 1, 0, out)
	close(out)

	require.NoError(t, err)
	assert.Len(t, syncCalls(runner.calls), 1)
}

func TestRcloneManager_Sync_FatalExitIsNotRetried(t *testing.T) {
	t.Parallel()

	repo, keys := makeLocalRepo(t, 10)
	runCalls := 0
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			runCalls++
			// What execRunner.Run returns when rclone hits --max-delete
			// (OBSERVED: rclone v1.60.1 and v1.75.1 exit 7, "Fatal error
			// received - not attempting retries").
			return fmt.Errorf("rclone exited: %w", fakeExitError{code: 7})
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return remoteListing(keys, 0), nil
		},
	}

	// The deadline only bounds a regression: a retried attempt waits 30s
	// first, so a retry shows up as DeadlineExceeded instead of a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := drainLines()
	err := testRcloneManager(runner).Sync(ctx, repo, "r", "p", 4, 3, 0, out)
	close(out)

	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded, "exit 7 must return at once, not wait to retry")
	assert.True(t, isExitCode(err, 7), "the rclone exit code must stay readable: %v", err)
	assert.Equal(t, 1, runCalls, "a fatal rclone exit must not be retried: each attempt deletes up to --max-delete more")
}

func TestRcloneManager_Sync_OtherFailuresAreStillRetried(t *testing.T) {
	t.Parallel()

	repo, keys := makeLocalRepo(t, 10)
	runCalls := 0
	runner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			runCalls++
			return fmt.Errorf("rclone exited: %w", fakeExitError{code: 5}) // temporary error
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return remoteListing(keys, 0), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	out := drainLines()
	err := testRcloneManager(runner).Sync(ctx, repo, "r", "p", 4, 3, 0, out)
	close(out)

	// Exit 5 waits to retry, so the short deadline ends it while waiting.
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, runCalls)
}

func TestRcloneManager_Sync_PreflightListsRemoteRecursively(t *testing.T) {
	t.Parallel()

	repo, keys := makeLocalRepo(t, 2)
	runner := &fakeRunner{outputData: remoteListing(keys, 0)}
	out := drainLines()
	require.NoError(t, testRcloneManager(runner).Sync(context.Background(), repo, "--log-file=/x", "p", 4, 1, 0, out))
	close(out)

	require.NotEmpty(t, runner.calls)
	assert.Equal(t, []string{"lsf", "-R", "--files-only", "--", "--log-file=/x:p"}, runner.calls[0].Args)
}

func TestRcloneManager_Sync_RefusesWhenRemoteCannotBeListed(t *testing.T) {
	t.Parallel()

	repo, _ := makeLocalRepo(t, 2)
	runner := &fakeRunner{outputErr: fmt.Errorf("rclone: %w", fakeExitError{code: 1})}
	out := drainLines()
	err := testRcloneManager(runner).Sync(context.Background(), repo, "r", "p", 4, 1, 0, out)
	close(out)

	require.Error(t, err)
	assert.Empty(t, syncCalls(runner.calls), "an unlisted remote cannot be shown to be within the cap: fail closed")
}

func TestRcloneManager_Sync_RemoteNotFoundCountsAsEmpty(t *testing.T) {
	t.Parallel()

	// A first-ever sync: rclone's own exit 3, "directory not found".
	repo, _ := makeLocalRepo(t, 2)
	runner := &fakeRunner{outputErr: fmt.Errorf("rclone: %w", fakeExitError{code: 3})}
	out := drainLines()
	require.NoError(t, testRcloneManager(runner).Sync(context.Background(), repo, "r", "p", 4, 1, 0, out))
	close(out)
	assert.Len(t, syncCalls(runner.calls), 1)
}

func TestRcloneManager_Sync_MissingLocalRepoCountsEveryRemoteFile(t *testing.T) {
	t.Parallel()

	// A local repository directory that is gone entirely has zero files, so
	// every remote file would be deleted; the count must say so rather than
	// the walk error being skipped.
	missing := filepath.Join(t.TempDir(), "gone")
	runner := &fakeRunner{outputData: remoteListing(nil, 101)}
	out := drainLines()
	err := testRcloneManager(runner).Sync(context.Background(), missing, "r", "p", 4, 1, 0, out)
	close(out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "101 remote files")
	assert.Empty(t, syncCalls(runner.calls))
}

func TestRcloneManager_Sync_ConfirmedCountLetsALargerDeleteThrough(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		allow       int
		wantRefused bool
	}{
		{"confirmed count covers it", 150, false},
		{"confirmed count is exactly it", 101, false},
		{"remote changed after the confirmation", 100, true},
		{"no confirmation", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, keys := makeLocalRepo(t, 10) // cap = floor of 100
			runner := &fakeRunner{outputData: remoteListing(keys, 101)}
			out := drainLines()
			err := testRcloneManager(runner).Sync(context.Background(), repo, "r", "p", 4, 1, tc.allow, out)
			close(out)

			if tc.wantRefused {
				var capErr *SyncDeleteCapError
				require.ErrorAs(t, err, &capErr)
				assert.Equal(t, SyncDeleteCapError{RemoteOnly: 101, Cap: 100, Allowed: tc.allow}, *capErr)
				assert.Empty(t, syncCalls(runner.calls))
				return
			}
			require.NoError(t, err)
			calls := syncCalls(runner.calls)
			require.Len(t, calls, 1)
			assert.True(t, argPairContains(calls[0].Args, "--max-delete", fmt.Sprint(tc.allow)),
				"the backstop carries the confirmed count: %q", calls[0].Args)
		})
	}
}

func TestRcloneManager_PreflightSync_ReportsCountAndCap(t *testing.T) {
	t.Parallel()

	repo, keys := makeLocalRepo(t, 600)
	runner := &fakeRunner{outputData: remoteListing(keys, 7)}
	pf, err := testRcloneManager(runner).PreflightSync(context.Background(), repo, "", "")
	require.NoError(t, err)
	assert.Equal(t, SyncPreflight{RemoteOnly: 7, Cap: 120}, pf)
	require.Len(t, runner.calls, 1, "a pre-flight only lists")
	assert.Equal(t, []string{"lsf", "-R", "--files-only", "--", "myremote:backup/path"}, runner.calls[0].Args)
}

// TestSync_PostBackupSyncCannotCarryAConfirmedDelete pins that only a manual
// RunSync can pass a confirmed count: against the same remote, the
// post-backup sync refuses and the manual sync with the count proceeds.
func TestSync_PostBackupSyncCannotCarryAConfirmedDelete(t *testing.T) {
	t.Parallel()

	// buildSvc's repository (/tmp/test-repo) does not exist here, so every
	// remote file is remote-only and the cap is the floor of 100.
	listing := remoteListing(nil, 150)

	t.Run("post-backup sync refuses", func(t *testing.T) {
		t.Parallel()
		db := newBackupTestDB(t)
		require.NoError(t, db.SetSetting("backup_sync_after", "true"))
		require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
		rclone := &fakeRunner{outputData: listing}
		svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, &uwfuRunner{}, rclone)
		seedStack(t, db, uwfuStack, "hot")

		run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
		require.NoError(t, err)

		stored, _ := readRun(t, db, run.ID)
		assert.Equal(t, "partial", stored.Status)
		assert.Contains(t, stored.ErrorMessage, "post-backup sync failed: refusing rclone sync: it would delete 150 remote files, cap is 100")
		assert.Empty(t, syncCalls(rclone.calls))
	})

	t.Run("manual sync with the confirmed count proceeds (control)", func(t *testing.T) {
		t.Parallel()
		db := newBackupTestDB(t)
		require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
		rclone := &fakeRunner{outputData: listing}
		svc := buildSvc(t, db, &fakeDocker{}, &uwfuRunner{}, rclone)

		out := drainLines()
		err := svc.RunSync(context.Background(), 150, out)
		close(out)
		require.NoError(t, err)
		calls := syncCalls(rclone.calls)
		require.Len(t, calls, 1)
		assert.True(t, argPairContains(calls[0].Args, "--max-delete", "150"))
	})
}
