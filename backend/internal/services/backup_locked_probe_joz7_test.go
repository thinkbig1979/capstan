package services

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-joz7: a repository probe that exits 11 (restic: "repository is
// already locked") is a lock, and the consumers of CheckRepository say so
// instead of reading it as a different fault. ---

// probeScriptRunner answers each `restic snapshots` probe from a script, one
// exit code per probe in order (0 = success, and success once the script
// runs out). Every other call succeeds; Output answers "config" so an rclone
// source probe passes. It records restic subcommands in order.
type probeScriptRunner struct {
	mu     sync.Mutex
	probes []int
	restic []string
}

func (r *probeScriptRunner) Run(_ context.Context, name string, args []string, _ []string, _ chan<- StreamLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name != "restic" {
		return nil
	}
	r.restic = append(r.restic, args[0])
	if args[0] != "snapshots" || len(r.probes) == 0 {
		return nil
	}
	code := r.probes[0]
	r.probes = r.probes[1:]
	if code == 0 {
		return nil
	}
	return fmt.Errorf("restic exited: %w", fakeExitError{code: code})
}

func (r *probeScriptRunner) Output(context.Context, string, []string, []string) ([]byte, error) {
	return []byte("config"), nil
}

func (r *probeScriptRunner) subcommands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.restic...)
}

// EnsureRepository creates a repository only when the probe says none exists
// (exit 10). A locked (11) or wrong-password (12) repository exists, and any
// other failure says nothing about whether one does, so none of them may
// reach `restic init`.
func TestResticManager_EnsureRepository_InitsOnlyWhenTheRepositoryDoesNotExist(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		code     int
		wantInit bool
	}{
		{"does not exist", resticExitRepoDoesNotExist, true},
		{"locked", resticExitRepoLocked, false},
		{"wrong password", resticExitWrongPassword, false},
		{"other failure", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &probeScriptRunner{probes: []int{tc.code}}
			m := newResticManagerWithRunner(testBackupConfig(), r, nil)

			err := m.EnsureRepository(context.Background())
			if tc.wantInit {
				require.NoError(t, err)
				assert.Equal(t, []string{"snapshots", "init"}, r.subcommands())
				return
			}
			require.Error(t, err)
			assert.True(t, isExitCode(err, tc.code), "the probe's exit code stays in the chain: %v", err)
			assert.Equal(t, []string{"snapshots"}, r.subcommands(), "an existing or unreadable repository must never be initialised over")
		})
	}
}

// A DR restore copies the remote repository, lock files included, so the
// assembled repository can carry a stale lock. RunDRRestore holds the global
// backup lock, so it clears stale locks and probes once more.
func TestRunDRRestore_StaleLockOnTheRestoredRepositoryIsClearedAndRechecked(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	r := &probeScriptRunner{probes: []int{resticExitRepoLocked, 0}}
	svc := buildSvc(t, db, &fakeDocker{}, r, r)

	out := make(chan StreamLine, 128)
	require.NoError(t, svc.RunDRRestore(context.Background(), out))
	assert.Equal(t, []string{"snapshots", "unlock", "snapshots"}, r.subcommands())
}

// A lock that survives `restic unlock` belongs to a live process: reported as
// a lock, never as a repository that does not open.
func TestRunDRRestore_LiveLockIsReportedAsALock(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	r := &probeScriptRunner{probes: []int{resticExitRepoLocked, resticExitRepoLocked}}
	svc := buildSvc(t, db, &fakeDocker{}, r, r)

	out := make(chan StreamLine, 128)
	err := svc.RunDRRestore(context.Background(), out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locked by another restic process")
	assert.NotContains(t, err.Error(), "does not open")
	assert.Contains(t, err.Error(), "pre-dr-", "the error still points at the directory holding displaced files")
	assert.True(t, isExitCode(err, resticExitRepoLocked))
	assert.Equal(t, []string{"snapshots", "unlock", "snapshots"}, r.subcommands(), "one retry, never a loop")
}

// Control: a repository that does not open (wrong password) is still reported
// that way, with no unlock.
func TestRunDRRestore_RepositoryThatDoesNotOpenIsNotUnlocked(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	r := &probeScriptRunner{probes: []int{resticExitWrongPassword}}
	svc := buildSvc(t, db, &fakeDocker{}, r, r)

	out := make(chan StreamLine, 128)
	err := svc.RunDRRestore(context.Background(), out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not open")
	assert.Equal(t, []string{"snapshots"}, r.subcommands())
}

// runSyncInternal names a locked repository as locked, not as one that is not
// accessible.
func TestRunSync_LockedLocalRepositoryIsReportedAsLocked(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	r := &probeScriptRunner{probes: []int{resticExitRepoLocked}}
	svc := buildSvc(t, db, &fakeDocker{}, r, r)

	out := make(chan StreamLine, 64)
	err := svc.RunSync(context.Background(), 0, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local repository is locked")
	assert.NotContains(t, err.Error(), "not accessible")
	assert.True(t, isExitCode(err, resticExitRepoLocked))
	assert.Equal(t, []string{"snapshots"}, r.subcommands(), "no rclone and no restic call after a refused probe")
}
