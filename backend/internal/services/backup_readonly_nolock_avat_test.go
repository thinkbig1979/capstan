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

// --- agent-os-avat: Capstan's read-only restic calls run with --no-lock, so a
// status poll or a snapshot listing never holds the shared lock a concurrent
// forget, prune or check needs to take the exclusive one; and the status
// probes are not re-run while a global operation holds the repository. ---

// Every read-only restic call ResticManager makes. A new one belongs here.
func TestAvat_EveryReadOnlyResticCallCarriesNoLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	calls := map[string]func(m *ResticManager){
		"CheckRepository (snapshots --quiet)": func(m *ResticManager) { _ = m.CheckRepository(ctx) },
		"ListSnapshots (snapshots --json)":    func(m *ResticManager) { _, _ = m.ListSnapshots(ctx, "app", 0) },
		"Stats (stats --mode raw-data)":       func(m *ResticManager) { _, _ = m.Stats(ctx) },
		"RestorePreview (ls)":                 func(m *ResticManager) { _ = m.RestorePreview(ctx, "abcd1234", nil) },
		"Verify (ls)":                         func(m *ResticManager) { _, _ = m.Verify(ctx, "abcd1234") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := &fakeRunner{outputData: []byte(`{"total_size":1}`)}
			call(newResticManagerWithRunner(testBackupConfig(), r, nil))
			require.Len(t, r.calls, 1)
			assert.Contains(t, r.calls[0].Args, "--no-lock", "argv %v", r.calls[0].Args)
		})
	}
}

// Control on the same instrument: a call that changes the repository keeps
// its lock. --no-lock on forget or backup would let two writers race.
func TestAvat_LockingCallsKeepTheirLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), r, nil)
	_ = m.ApplyRetention(ctx, "app", nil)
	_ = m.Prune(ctx, false, nil)
	require.NotEmpty(t, r.calls)
	for _, c := range r.calls {
		assert.NotContains(t, c.Args, "--no-lock", "argv %v", c.Args)
	}
}

// While a global operation runs, the status probes answer from the last
// probe instead of contacting the repository again.
func TestAvat_StatusProbesAreSkippedWhileBusy(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	r := &fakeRunner{outputData: []byte(`{"total_size":42}`)}
	svc := buildSvc(t, db, &fakeDocker{}, r, r)
	ctx := context.Background()

	first := svc.CheckRepository(ctx)
	require.Equal(t, RepoStateOK, first.RepoState)
	size := svc.RepoSizeBytes(ctx)
	require.NotNil(t, size)
	require.Len(t, r.calls, 2, "idle: one probe and one stats call")

	svc.ForceSetBusy(true)
	busy := svc.CheckRepository(ctx)
	busySize := svc.RepoSizeBytes(ctx)
	assert.Len(t, r.calls, 2, "busy: no restic call at all")
	assert.Equal(t, first, busy, "busy: the last probe result")
	require.NotNil(t, busySize)
	assert.Equal(t, int64(42), *busySize)

	svc.ForceSetBusy(false)
	svc.CheckRepository(ctx)
	assert.Len(t, r.calls, 3, "idle again: the probe runs")
}

// With nothing cached, a busy status poll still probes: there is no earlier
// answer to give.
func TestAvat_BusyWithNoEarlierProbeStillProbes(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	r := &fakeRunner{outputData: []byte(`{"total_size":42}`)}
	svc := buildSvc(t, db, &fakeDocker{}, r, r)
	svc.ForceSetBusy(true)

	assert.Equal(t, RepoStateOK, svc.CheckRepository(context.Background()).RepoState)
	assert.NotNil(t, svc.RepoSizeBytes(context.Background()))
	assert.Len(t, r.calls, 2)
}

// The real restic, two-sided: with the repository's locks directory
// read-only, `snapshots` fails because it cannot write its lock, and
// `snapshots --no-lock` succeeds, so --no-lock is what keeps it off the lock.
func TestAvat_RealRestic_NoLockWritesNoLock(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed; the real-lock arm cannot run")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only locks directory this arm relies on")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	t.Setenv("RESTIC_CACHE_DIR", filepath.Join(dir, "restic-cache"))
	cfg := testBackupConfig()
	cfg.ResticRepository = repo
	cfg.ResticPassword = "avat-test-password"
	m := NewResticManager(cfg, nil)
	ctx := context.Background()
	require.NoError(t, m.EnsureRepository(ctx))

	locks := filepath.Join(repo, "locks")
	require.NoError(t, os.Chmod(locks, 0o555))       //nolint:gosec // G302: a directory under this test's t.TempDir(), made read-only on purpose
	t.Cleanup(func() { _ = os.Chmod(locks, 0o755) }) //nolint:gosec // G302: restored so t.TempDir() can remove it

	pwFile, cleanup, err := m.withPasswordFile()
	require.NoError(t, err)
	defer cleanup()
	run := func(args ...string) error {
		return runDrained(ctx, &execRunner{}, "restic", args, m.resticEnv(pwFile))
	}
	require.Error(t, run("snapshots", "--quiet"), "control: a locking read needs to write its lock")
	require.NoError(t, run("snapshots", "--no-lock", "--quiet"), "--no-lock writes no lock")

	// And the production probe, which must be the --no-lock form.
	require.NoError(t, m.CheckRepository(ctx))
}
