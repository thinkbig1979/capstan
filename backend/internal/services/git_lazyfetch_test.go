package services

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// This file pins agent-os-taa7: in a partial clone, a read route that needs a
// missing object fetched it from the promisor remote, which runs the
// repository's own transport program (here remote.origin.uploadpack). Read
// children now run with GIT_NO_LAZY_FETCH=1, so a missing object fails or
// hides; pull, commit and push still fetch.

// lazyFix is a blob:none partial clone of src over file://, with HEAD moved
// (reset --soft) to a commit whose changed blobs were never fetched, and a
// marker uploadpack that records each run and then serves the fetch.
type lazyFix struct {
	svc      *GitService
	src, top string
	marks    string
	c1, c2   string
}

// lazyFetchMinGit is the first git that honours GIT_NO_LAZY_FETCH: its
// release notes (Documentation/RelNotes/2.45.0.txt) introduce
// `git --no-lazy-fetch`, and 2.44.x's notes do not mention it.
const lazyFetchMinMajor, lazyFetchMinMinor = 2, 45

// newLazyFix builds the fixture. rename makes c2 also rename a.txt to b.txt
// with an edit, so status's inexact rename detection needs c2's b.txt blob;
// without it c2 only modifies x, which status answers from object ids alone.
// pullable leaves HEAD at c1 with a clean work tree instead, for a pull.
func newLazyFix(t *testing.T, rename, pullable bool) *lazyFix {
	t.Helper()
	if ok, v := gitAtLeast(t, lazyFetchMinMajor, lazyFetchMinMinor); !ok {
		t.Skipf("git %s ignores GIT_NO_LAZY_FETCH (needs 2.45 or later), so a read route there still lazy-fetches", v)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	base := t.TempDir()
	f := &lazyFix{src: filepath.Join(base, "src"), top: filepath.Join(base, "work"), marks: filepath.Join(base, "marks")}
	runGit(t, base, "init", "-q", "-b", "main", f.src)
	runGit(t, f.src, "config", "uploadpack.allowFilter", "true")
	runGit(t, f.src, "config", "uploadpack.allowAnySHA1InWant", "true")
	writeFile(t, filepath.Join(f.src, "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(f.src, "x"), "x\n")
	writeFile(t, filepath.Join(f.src, "a.txt"), "one\ntwo\nthree\nfour\nfive\n")
	runGit(t, f.src, "add", "-A")
	runGit(t, f.src, "commit", "-q", "-m", "c1")
	f.c1 = runGit(t, f.src, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(f.src, "x"), "LAZY_X\n")
	if rename {
		runGit(t, f.src, "mv", "a.txt", "b.txt")
		writeFile(t, filepath.Join(f.src, "b.txt"), "one\ntwo\nLAZY_B\nfour\nfive\n")
	}
	runGit(t, f.src, "add", "-A")
	runGit(t, f.src, "commit", "-q", "-m", "c2")
	f.c2 = runGit(t, f.src, "rev-parse", "HEAD")

	runGit(t, base, "clone", "-q", "--filter=blob:none", "--no-checkout", "file://"+f.src, f.top)
	runGit(t, f.top, "checkout", "-q", "-B", "main", f.c1) // fetches c1's blobs, before the marker exists
	runGit(t, f.top, "branch", "-q", "--set-upstream-to=origin/main")
	if !pullable {
		runGit(t, f.top, "reset", "-q", "--soft", "origin/main")
	}
	//nolint:gosec // a program git runs must be executable; test-owned file under t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(base, "uploadpack"),
		[]byte("#!/bin/sh\necho ran >> '"+f.marks+"'\nexec git-upload-pack \"$@\"\n"), 0o755))
	runGit(t, f.top, "config", "remote.origin.uploadpack", filepath.Join(base, "uploadpack"))

	db, err := database.NewWithMigrationsAndEncryptor(t.TempDir(), NewTokenEncryptorOrDefault("", "test-secret-key-for-unit-tests-32c"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: f.top, Name: filepath.Base(f.top)}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-lazy", Directory: f.top, ComposeFile: "compose.yaml", EnvFile: ".env", ProjectName: "lazy"}))
	f.svc = NewGitService(&config.Config{}, db)
	f.svc.SetOperationLock(NewOperationLock())
	return f
}

// runs reports how many times the marker uploadpack ran since the last call,
// and resets the count.
func (f *lazyFix) runs(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(f.marks)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	require.NoError(t, os.Remove(f.marks))
	return strings.Count(string(b), "ran\n")
}

// requireMissing fails unless the blob at HEAD:path is absent locally, so a
// silent route cannot pass on a fixture that never needed a fetch.
func (f *lazyFix) requireMissing(t *testing.T, path string) {
	t.Helper()
	oid := runGit(t, f.top, "rev-parse", "HEAD:"+path)
	out, err := gitTry(t, f.top, "--no-lazy-fetch", "cat-file", "-e", oid)
	require.Error(t, err, "HEAD:%s (%s) is present locally, so this fixture cannot need a lazy fetch: %s", path, oid, out)
}

// requireControlFetches runs a raw git read with no Capstan argv or env and
// requires it to run the marker: the fixture can fire, so a silent route is a
// real result. It runs after the route, because the control fetches the blob.
// The routes' controls are the remedy objectMissingMessage names, and each
// test then requires the route to succeed, which proves the remedy works.
func (f *lazyFix) requireControlFetches(t *testing.T, args ...string) {
	t.Helper()
	out, err := gitTry(t, f.top, args...)
	t.Logf("control `git %s`: err=%v\n%s", strings.Join(args, " "), err, out)
	require.Positive(t, f.runs(t), "control: raw `git %s` did not lazy-fetch on this fixture, so a silent route proves nothing", strings.Join(args, " "))
}

// requireObjectMissing requires the refused read to answer 409
// GIT_OBJECT_MISSING with a message naming the cause, not a bare 500.
func requireObjectMissing(t *testing.T, err error) {
	t.Helper()
	var appErr *models.AppError
	require.True(t, errors.As(err, &appErr), "want a 409 GIT_OBJECT_MISSING, got a plain error (a bare 500): %v", err)
	require.Equal(t, http.StatusConflict, appErr.Status)
	require.Equal(t, models.ErrGitObjectMissing, appErr.Code)
	require.Equal(t, objectMissingMessage, appErr.Message)
	for _, want := range []string{"partial clone", "`git diff HEAD`", "`git show <commit>`", "without --filter"} {
		require.Contains(t, appErr.Message, want)
	}
	// Remedies that download nothing (git fetch, an up-to-date pull) and the
	// read-only framing that is false on pull and commit.
	for _, wrong := range []string{"git fetch", "Pull the stack", "read view"} {
		require.NotContains(t, appErr.Message, wrong)
	}
}

// TestLazyFetch_GetStatus: status's rename detection reads the HEAD blob of
// the renamed file. The route now answers 409 GIT_OBJECT_MISSING instead of
// fetching.
func TestLazyFetch_GetStatus(t *testing.T) {
	f := newLazyFix(t, true, false)
	f.requireMissing(t, "b.txt")

	_, err := f.svc.GetStatus(f.top)
	t.Logf("GetStatus with HEAD:b.txt missing: %v", err)
	require.Zero(t, f.runs(t), "GetStatus lazy-fetched, running the repository's remote.origin.uploadpack")
	requireObjectMissing(t, err)

	f.requireControlFetches(t, "diff", "HEAD")
	_, err = f.svc.GetStatus(f.top)
	require.NoError(t, err, "after the message's remedy, GetStatus must succeed")
}

// TestLazyFetch_GetChanges: the commit dialog's list runs status first and
// then `status --porcelain=v2`, both of which detect renames.
func TestLazyFetch_GetChanges(t *testing.T) {
	f := newLazyFix(t, true, false)
	f.requireMissing(t, "b.txt")

	_, err := f.svc.GetChanges(f.top)
	t.Logf("GetChanges with HEAD:b.txt missing: %v", err)
	require.Zero(t, f.runs(t), "GetChanges lazy-fetched, running the repository's remote.origin.uploadpack")
	requireObjectMissing(t, err)

	f.requireControlFetches(t, "diff", "HEAD")
	_, err = f.svc.GetChanges(f.top)
	require.NoError(t, err, "after the message's remedy, GetChanges must succeed")
}

// TestLazyFetch_GetChangeDiff: no rename, so status and the change list need
// no blob and succeed; only `diff HEAD -- x` needs c2's x. The route answers
// 409 GIT_OBJECT_MISSING instead of fetching it.
func TestLazyFetch_GetChangeDiff(t *testing.T) {
	f := newLazyFix(t, false, false)
	f.requireMissing(t, "x")

	_, err := f.svc.GetChangeDiff(f.top, "x")
	t.Logf("GetChangeDiff with HEAD:x missing: %v", err)
	require.Zero(t, f.runs(t), "GetChangeDiff lazy-fetched, running the repository's remote.origin.uploadpack")
	requireObjectMissing(t, err)

	f.requireControlFetches(t, "diff", "HEAD")
	_, err = f.svc.GetChangeDiff(f.top, "x")
	require.NoError(t, err, "after the message's remedy, GetChangeDiff must succeed")
}

// TestLazyFetch_LogRoutes: GetLog, GetLogForFile and GetDiff on the same
// fixture. No plain `git log` trigger is known (log.follow is pinned off by
// gitHardeningArgs), so for the two log routes this pins the outcome rather
// than discriminating it; the env guard below is what keeps them covered.
func TestLazyFetch_LogRoutes(t *testing.T) {
	f := newLazyFix(t, true, false)
	f.requireMissing(t, "b.txt")

	_, err := f.svc.GetLog(f.top, 10, 0)
	require.NoError(t, err, "GetLog needs no blob")
	require.Zero(t, f.runs(t), "GetLog lazy-fetched")
	_, err = f.svc.GetLogForFile(f.top, "b.txt", 10)
	require.NoError(t, err, "GetLogForFile needs no blob")
	require.Zero(t, f.runs(t), "GetLogForFile lazy-fetched")
	_, err = f.svc.GetDiff(f.top, f.c2)
	t.Logf("GetDiff of c2 with its blobs missing: %v", err)
	require.Zero(t, f.runs(t), "GetDiff lazy-fetched")

	f.requireControlFetches(t, "show", f.c2)
	_, err = f.svc.GetDiff(f.top, f.c2)
	require.NoError(t, err, "after the message's remedy, GetDiff must succeed")
}

// TestLazyFetch_PullStillFetches: pull's checkout needs the blobs it moves to,
// so pull must keep lazy fetch. HEAD is c1 with a clean tree and origin is at
// c2: the pull must land on c2 with c2's content in the work tree.
func TestLazyFetch_PullStillFetches(t *testing.T) {
	f := newLazyFix(t, true, true)

	ar, res := f.svc.PullVerified(f.top, false, nil)
	require.NotNil(t, res, "pull failed: %s %s", ar.Outcome, ar.Reason)
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, ar.Reason)
	require.Equal(t, f.c2, res.CurrentCommit)
	b, err := os.ReadFile(filepath.Join(f.top, "b.txt"))
	require.NoError(t, err)
	require.Contains(t, string(b), "LAZY_B", "the pull did not check out c2's blob")
	t.Logf("uploadpack runs during the pull: %d", f.runs(t))
}

// TestLazyFetch_GitSubcommand: the opt-out is keyed on the subcommand, so the
// parser must skip every leading global option and its value, including
// Capstan's own prefix, and never read a value as the subcommand.
func TestLazyFetch_GitSubcommand(t *testing.T) {
	prefix := append([]string{"-c", "safe.directory=/x"}, gitHardeningArgs...)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "core.fsmonitor=false", "status"}, "status"},
		{[]string{"-c", "alias.x=push", "status"}, "status"},
		{[]string{"-C", "push", "log"}, "log"},
		{[]string{"--git-dir", "commit", "rev-parse"}, "rev-parse"},
		{[]string{"--work-tree", "pull", "--namespace", "push", "--config-env", "a=pull", "diff"}, "diff"},
		{[]string{"--git-dir=/g", "--no-optional-locks", "ls-files"}, "ls-files"},
		{append(append([]string{}, prefix...), changeDiffArgs...), "diff"},
		{append(append([]string{}, prefix...), "pull", "--ff-only"), "pull"},
		{append(append([]string{}, prefix...), "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false", "commit", "--only"), "commit"},
		{append(append([]string{}, prefix...), "-c", "push.gpgSign=false", "push", "--porcelain"), "push"},
		{[]string{"--attr-source", "push", "--shallow-file", "pull", "status"}, "status"},
		{[]string{"-c", "x=y"}, "command"},
	} {
		require.Equal(t, tc.want, gitSubcommand(tc.args), "%q", tc.args)
	}
}

// TestLazyFetch_EnvGuard: every git child the package builds comes from
// gitCmdWithCreds (TestZ0gl_GitChildrenOnlyFromGitCmdWithCreds). This pins
// that the helper gives every subcommand GIT_NO_LAZY_FETCH=1 except the three
// that need lazy fetch, so a new read child cannot run without it.
func TestLazyFetch_EnvGuard(t *testing.T) {
	require.Equal(t, map[string]bool{"pull": true, "commit": true, "push": true}, lazyFetchSubcommands,
		"only pull, commit and push may lazy-fetch; a read added here runs the repository's transport programs")

	s := NewGitService(&config.Config{}, nil)
	lastValue := func(args ...string) string {
		cmd, _ := s.gitCmdWithCreds(context.Background(), t.TempDir(), gitCreds{}, args...)
		v := ""
		for _, e := range cmd.Env {
			if strings.HasPrefix(e, "GIT_NO_LAZY_FETCH=") {
				v = strings.TrimPrefix(e, "GIT_NO_LAZY_FETCH=")
			}
		}
		return v
	}
	// Every read subcommand the package runs, and the production argv of the
	// commit dialog's diff.
	for _, args := range [][]string{
		{"status", "--porcelain"}, {"-c", "core.fsmonitor=false", "--no-optional-locks", "status", "--porcelain=v2"},
		{"diff", "--name-only"}, {"diff-tree", "-r"}, {"log", "-1"}, {"show", "HEAD"},
		{"rev-parse", "HEAD"}, {"rev-list", "--count", "HEAD"}, {"symbolic-ref", "-q", "HEAD"},
		{"ls-files", "-s"}, {"ls-tree", "HEAD"}, {"cat-file", "-s", "x"}, {"config", "--get-all", "k"},
		{"remote", "get-url", "origin"}, {"ls-remote", "--get-url"}, {"merge-base", "--is-ancestor", "a", "b"},
		{"-c", "alias.x=push", "status"},
		changeDiffArgs,
	} {
		require.Equal(t, "1", lastValue(args...), "git %q must run with GIT_NO_LAZY_FETCH=1", args)
	}
	for _, sub := range []string{"pull", "commit", "push"} {
		require.Empty(t, lastValue("-c", "core.hooksPath=/dev/null", sub), "git %s needs lazy fetch and must not carry GIT_NO_LAZY_FETCH", sub)
	}
}
