package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// newEnvScopeFix is the shared repository (stacks app and other) with
// app/compose.yaml naming sub/secrets.conf as an env_file, other/compose.yaml
// naming ../app/b.conf, and app/sub/plain.txt named by nothing. All three are
// changed and uncommitted. Not one of them has an env name, so only the
// compose files say they are env files.
func newEnvScopeFix(t *testing.T) *commitFix {
	t.Helper()
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: x\n    env_file: sub/secrets.conf\n")
	writeFile(t, filepath.Join(f.sibling, "compose.yaml"), "services:\n  other:\n    image: y\n    env_file: ../app/b.conf\n")
	for _, n := range []string{"sub/secrets.conf", "sub/plain.txt", "b.conf"} {
		writeFile(t, filepath.Join(f.dir, n), "0\n")
	}
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "env files")
	for _, n := range []string{"sub/secrets.conf", "sub/plain.txt", "b.conf"} {
		writeFile(t, filepath.Join(f.dir, n), "1\n")
	}
	return f
}

// assertEnvScope checks, for a request from dir, that every path in hidden is
// IsEnv in GET /git/changes and Hidden with no diff in /git/changes/diff, and
// that every path in shown is neither: a rule that hid everything would fail
// the shown half.
func assertEnvScope(t *testing.T, f *commitFix, dir string, hidden, shown []string) {
	t.Helper()
	got, err := f.svc.GetChanges(dir)
	require.NoError(t, err)
	byPath := map[string]models.GitChange{}
	for _, c := range got.Changes {
		byPath[c.Path] = c
	}
	require.ElementsMatch(t, append(append([]string{}, hidden...), shown...), changePaths(got))
	for _, p := range hidden {
		assert.True(t, byPath[p].IsEnv, "%s is named by a compose env_file, so it is an env file", p)
		d, err := f.svc.GetChangeDiff(dir, p)
		require.NoError(t, err)
		assert.True(t, d.Hidden, "%s: the diff must be hidden", p)
		assert.Empty(t, d.Diff, "%s: no diff content may be served", p)
	}
	for _, p := range shown {
		assert.False(t, byPath[p].IsEnv, "control: %s is named by nothing", p)
		d, err := f.svc.GetChangeDiff(dir, p)
		require.NoError(t, err)
		assert.False(t, d.Hidden, "control: %s is shown", p)
		assert.Contains(t, d.Diff, "+1", "control: %s's diff is served", p)
	}
}

// agent-os-0zjs: the env files come from every stack inside the repository,
// each stack's entries read from its own folder, whatever folder the request
// names.
func TestGetChanges_EnvFileScope_StackFolder(t *testing.T) {
	f := newEnvScopeFix(t)
	assertEnvScope(t, f, f.dir, []string{"app/sub/secrets.conf", "app/b.conf"}, []string{"app/sub/plain.txt"})
}

func TestGetChanges_EnvFileScope_Subfolder(t *testing.T) {
	f := newEnvScopeFix(t)
	assertEnvScope(t, f, filepath.Join(f.dir, "sub"), []string{"app/sub/secrets.conf"}, []string{"app/sub/plain.txt"})
}

func TestGetChanges_EnvFileScope_RepoTop(t *testing.T) {
	f := newEnvScopeFix(t)
	assertEnvScope(t, f, f.top, []string{"app/sub/secrets.conf", "app/b.conf"}, []string{"app/sub/plain.txt"})
}

// A file in stack app's folder that only stack other's compose file names is
// hidden from a request for app's folder too.
func TestGetChanges_EnvFileScope_SiblingStackNamesFile(t *testing.T) {
	f := newEnvScopeFix(t)
	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	byPath := map[string]models.GitChange{}
	for _, c := range got.Changes {
		byPath[c.Path] = c
	}
	assert.True(t, byPath["app/b.conf"].IsEnv, "named by other/compose.yaml as ../app/b.conf")
	assert.False(t, byPath["app/sub/plain.txt"].IsEnv, "control: named by nothing")
	d, err := f.svc.GetChangeDiff(f.dir, "app/b.conf")
	require.NoError(t, err)
	assert.True(t, d.Hidden)
	assert.Empty(t, d.Diff)
}

// A stack whose folder can't be placed (an unreadable parent makes the
// containment check fail) may hold this repository's env files, so every
// change but the stacks' compose files is flagged, as in the history view.
func TestGetChanges_EnvFileScope_UnplaceableStack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not stop root from resolving the stack folder, so the containment check cannot fail")
	}
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "a.txt"), "1\n")
	writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: z\n")
	locked := filepath.Join(f.top, "locked")
	inner := filepath.Join(locked, "inner")
	require.NoError(t, os.MkdirAll(inner, 0o750))
	f.registerStack(t, inner, "s-locked", "locked")
	require.NoError(t, os.Chmod(locked, 0))                          //nolint:gosec // the test needs an unsearchable folder
	t.Cleanup(func() { assert.NoError(t, os.Chmod(locked, 0o750)) }) //nolint:gosec // restored so TempDir can remove it
	_, lerr := os.Lstat(inner)
	require.ErrorIs(t, lerr, os.ErrPermission, "the fixture must make the stack folder unresolvable")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err, "an unplaceable stack narrows this request; it does not fail it")
	byPath := map[string]models.GitChange{}
	for _, c := range got.Changes {
		byPath[c.Path] = c
	}
	require.ElementsMatch(t, []string{"app/a.txt", "app/compose.yaml"}, changePaths(got))
	assert.True(t, byPath["app/a.txt"].IsEnv, "the env files the unplaced stack names are unknown")
	assert.False(t, byPath["app/compose.yaml"].IsEnv, "a stack's own compose file stays exempt")
	d, err := f.svc.GetChangeDiff(f.dir, "app/a.txt")
	require.NoError(t, err)
	assert.True(t, d.Hidden)
	d, err = f.svc.GetChangeDiff(f.dir, "app/compose.yaml")
	require.NoError(t, err)
	assert.False(t, d.Hidden, "control: the compose file's diff is still served")
	assert.Contains(t, d.Diff, "image: z")
}
