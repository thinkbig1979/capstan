package services

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRcloneManager_PositionalsFollowEndOfOptions pins agent-os-tyl6: every
// rclone call puts "--" before its positionals and every flag before "--".
// The remote (and, for sync, the restic repository path) come from settings;
// OBSERVED with rclone v1.60.1, a positional such as "--log-file=/p:" is
// otherwise parsed as a flag and creates the file /p:.
func TestRcloneManager_PositionalsFollowEndOfOptions(t *testing.T) {
	t.Parallel()

	const remote = "--log-file=/tmp/tyl6"
	const repo = "--config=/tmp/tyl6"

	drain := func() chan StreamLine {
		out := make(chan StreamLine, 64)
		go func() {
			for range out {
			}
		}()
		return out
	}

	// wantTail is what must follow "--", in order; nothing before "--" may
	// be one of those values.
	check := func(t *testing.T, args []string, wantTail ...string) {
		t.Helper()
		i := slices.Index(args, "--")
		require.GreaterOrEqual(t, i, 1, "no end-of-options separator in %q", args)
		assert.Equal(t, wantTail, args[i+1:], "positionals after -- in %q", args)
		for _, a := range args[1:i] {
			assert.NotContains(t, wantTail, a, "a positional sits before -- in %q", args)
		}
	}

	t.Run("lsd (TestConnectivity)", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		require.NoError(t, testRcloneManager(runner).TestConnectivity(context.Background(), remote))
		check(t, runner.lastCall().Args, remote+":")
		assert.Equal(t, []string{"lsd", "--max-depth", "1"}, runner.lastCall().Args[:3])
	})

	t.Run("sync (Sync)", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		out := drain()
		require.NoError(t, testRcloneManager(runner).Sync(context.Background(), repo, remote, "p", 4, 1, out))
		close(out)
		args := runner.lastCall().Args
		check(t, args, repo, remote+":p")
		assert.Equal(t, append([]string{"sync"}, syncOptions(4)...), args[:len(args)-3], "every flag precedes --")
	})

	t.Run("lsf probe + sync (RestoreRepo)", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{outputData: []byte("config\n")}
		out := drain()
		require.NoError(t, testRcloneManager(runner).RestoreRepo(context.Background(), remote, "p", "/restore", "/bak", 1, out))
		close(out)
		require.Len(t, runner.calls, 2)
		check(t, runner.calls[0].Args, remote+":p/config")
		args := runner.calls[1].Args
		check(t, args, remote+":p", "/restore")
		i := slices.Index(args, "--")
		assert.Equal(t, "/bak", args[slices.Index(args, "--backup-dir")+1])
		assert.Less(t, slices.Index(args, "--backup-dir"), i, "--backup-dir precedes --")
	})

	t.Run("lsf (remoteHasSnapshots)", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		_, err := testRcloneManager(runner).remoteHasSnapshots(context.Background(), remote, "p")
		require.NoError(t, err)
		check(t, runner.lastCall().Args, remote+":p/snapshots")
	})
}
