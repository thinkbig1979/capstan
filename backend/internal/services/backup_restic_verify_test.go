package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ValidateVerifySubset ---

func TestValidateVerifySubset(t *testing.T) {
	t.Parallel()

	accepted := []struct{ in, want string }{
		{"", DefaultVerifyReadDataSubset},
		{"5%", "5%"},
		{"2.5%", "2.5%"},
		{"100%", "100%"},
		{"1/12", "1/12"},
		{"5G", "5G"},
		{"512M", "512M"},
	}
	for _, tc := range accepted {
		got, err := ValidateVerifySubset(tc.in)
		require.NoError(t, err, "subset %q must be accepted", tc.in)
		assert.Equal(t, tc.want, got)
	}

	// The subset is the only caller-supplied element of restic's argv on this
	// path, so anything that is not one of restic's three documented forms is
	// refused rather than passed through.
	rejected := []string{
		"all",
		"5",
		"-5%",
		"5%; rm -rf /",
		"--read-data",
		"1/",
		"%",
	}
	for _, in := range rejected {
		_, err := ValidateVerifySubset(in)
		assert.Error(t, err, "subset %q must be refused", in)
	}
}

// --- VerifyRepositoryData argv tests (fake runner; no restic is executed) ---

func TestResticManager_VerifyRepositoryData_DefaultSubsetArgv(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	require.NoError(t, m.VerifyRepositoryData(context.Background(), "", out))
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "restic", call.Binary)
	assert.Equal(t, "check", call.Args[0])
	assert.True(t, argContains(call.Args, "--read-data-subset="+DefaultVerifyReadDataSubset),
		"argv must carry the default subset, got %v", call.Args)
}

func TestResticManager_VerifyRepositoryData_ExplicitSubsetArgv(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	require.NoError(t, m.VerifyRepositoryData(context.Background(), "100%", out))
	close(out)

	assert.True(t, argContains(runner.lastCall().Args, "--read-data-subset=100%"))
}

func TestResticManager_VerifyRepositoryData_RejectsBadSubsetBeforeExec(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.VerifyRepositoryData(context.Background(), "bogus", out)
	close(out)

	require.Error(t, err)
	assert.Empty(t, runner.calls, "restic must not be invoked for an invalid subset")
}

// --- Real-restic integrity test (agent-os-j1jw) ---
//
// This is the arm that discriminates. The argv tests above prove the flag is
// SENT; only a real restic run proves the behaviour differs from
// CheckRepository's on a repository that has lost its data, and in particular
// that it differs WITH A WARM CACHE — which is the whole defect: the filer's
// v0.22.0 restore drill saw a restore report success against a repository with
// no pack files, because the read was served from /home/appuser/.cache/restic.
//
// Every arm below runs against the same repository with the same warm cache,
// so a failure cannot be explained by a broken binary path, a wrong password
// or a malformed argv: those would fail the intact arm too.
func TestResticManager_VerifyRepositoryData_RealRestic(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed; the real-integrity arm cannot run")
	}

	// t.Setenv forbids t.Parallel. The cache dir is redirected so the test
	// neither reads nor pollutes the developer's own restic cache — and so
	// "warm cache" below means a cache this test demonstrably filled.
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	src := filepath.Join(dir, "src")
	cache := filepath.Join(dir, "cache")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.MkdirAll(cache, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "payload.txt"), []byte("j1jw"), 0o600))
	t.Setenv("RESTIC_CACHE_DIR", cache)

	cfg := testBackupConfig()
	cfg.ResticRepository = repo
	cfg.ResticPassword = "j1jw-test-password"
	m := NewResticManager(cfg, nil)

	ctx := context.Background()
	drain := func() (chan StreamLine, func()) {
		out := make(chan StreamLine, 256)
		done := make(chan struct{})
		go func() {
			for range out {
			}
			close(done)
		}()
		return out, func() { close(out); <-done }
	}

	require.NoError(t, m.EnsureRepository(ctx), "repository init must succeed")

	out, closeOut := drain()
	_, err := m.Backup(ctx, src, []string{"j1jw"}, out)
	closeOut()
	require.NoError(t, err, "backup must succeed against an intact repository")

	// Warm the cache through the very probe this bead says is insufficient.
	require.NoError(t, m.CheckRepository(ctx), "metadata probe must pass while intact")
	entries, err := os.ReadDir(cache)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "cache must be warm for the cache arm to mean anything")

	// ARM 1 — intact repository PASSES. Runs first, so the failing arms below
	// cannot be explained by an environment in which this could never pass.
	out, closeOut = drain()
	err = m.VerifyRepositoryData(ctx, "100%", out)
	closeOut()
	require.NoError(t, err, "an intact repository must pass verification")

	// Break the repository exactly as the filer did: remove the pack files,
	// leave the snapshot metadata intact.
	packs := filepath.Join(repo, "data")
	moved := filepath.Join(dir, "data-moved")
	require.NoError(t, os.Rename(packs, moved))

	// ARM 2 — THE DEFECT. The existing metadata probe still passes, served
	// from the warm cache, against a repository that can restore nothing.
	// This assertion is what makes the next one meaningful; if it ever starts
	// failing, the cache arm is no longer being exercised.
	require.NoError(t, m.CheckRepository(ctx),
		"CheckRepository is expected to PASS on the broken repository (that is the defect); "+
			"if it now fails, the warm cache is no longer serving the metadata and the arm below proves less")

	// ARM 3 — the fix. Same repository, same warm cache, same instrument as
	// arm 1, opposite verdict.
	out, closeOut = drain()
	err = m.VerifyRepositoryData(ctx, "100%", out)
	closeOut()
	require.Error(t, err, "verification must FAIL on a repository whose pack files are gone, warm cache notwithstanding")
	assert.Contains(t, strings.ToLower(err.Error()), "integrity check failed")

	// ARM 4 — the default subset is not a weaker answer to THIS failure:
	// check reconciles the index against a backend LIST of pack files, which
	// no cache can serve, so a lost-data repository fails at any subset.
	out, closeOut = drain()
	err = m.VerifyRepositoryData(ctx, "", out)
	closeOut()
	require.Error(t, err, "the default subset must also fail on a repository whose pack files are gone")

	// ARM 5 — restore the packs; the same instrument passes again. Without
	// this the "fails" above is consistent with a check that always fails.
	require.NoError(t, os.Rename(moved, packs))
	out, closeOut = drain()
	err = m.VerifyRepositoryData(ctx, "100%", out)
	closeOut()
	require.NoError(t, err, "verification must pass again once the pack files are back")
}
