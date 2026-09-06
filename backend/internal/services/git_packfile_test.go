package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitPackedRepo builds a bare "remote", clones it, and runs `git gc --aggressive`
// on the clone so every object is packed. No network.
//
// The packing was originally load-bearing: the deleted go-git path only reached
// its packfile reader for packed objects, which is how agent-os-r1a survived 657
// tests — nothing here had ever opened a packed repository. Since agent-os-yo9e
// deleted that path, `git` itself reads packed and loose objects identically and
// the packing proves nothing on its own. The fixture is kept because the SHAPE
// is still the one that matters: a real stack is a clone with an upstream, and
// these tests are the only ones in the package that assert against one.
func gitPackedRepo(t *testing.T) (local, remote string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	base := t.TempDir()
	remote = filepath.Join(base, "remote.git")
	seed := filepath.Join(base, "seed")
	local = filepath.Join(base, "local")

	run := func(dir string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.invalid",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run(base, "init", "--bare", "--initial-branch=main", remote)

	// Seed via a normal repo pushed to the bare one. Cloning the bare repo
	// while it is still empty fails, so it cannot be the seed.
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	run(seed, "init", "--initial-branch=main")
	for _, msg := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(seed, "compose.yaml"), []byte("# "+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(seed, "add", ".")
		run(seed, "commit", "-m", msg)
	}
	run(seed, "remote", "add", "origin", remote)
	run(seed, "push", "-u", "origin", "main")

	run(base, "clone", "--branch", "main", remote, local)
	// Pack everything. Without this the clone may still hold loose objects and
	// the bug does not reproduce.
	run(local, "gc", "--aggressive", "--prune=now")

	packs, err := filepath.Glob(filepath.Join(local, ".git", "objects", "pack", "*.pack"))
	if err != nil || len(packs) == 0 {
		t.Fatalf("fixture is not packed (%d packfiles, err=%v) — the bug will not reproduce", len(packs), err)
	}
	return local, remote
}

// TestGetStatus_PackedRepo pins every field of the status contract against a
// clone with an upstream.
//
// It was the regression test for agent-os-r1a (a nil object cache panicked
// go-git on any packed repository) and asserted on getStatusGoGit directly,
// because the CLI fallback would otherwise have masked the failure. agent-os-yo9e
// deleted that path and with it the fallback, so there is nothing left to mask
// anything: the public entry point IS the implementation, and asserting through
// it is now both the honest and the stronger choice.
func TestGetStatus_PackedRepo(t *testing.T) {
	local, _ := gitPackedRepo(t)
	s := &GitService{}

	st, err := s.GetStatus(local)
	if err != nil {
		t.Fatalf("GetStatus failed on a packed repository: %v", err)
	}

	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if st.Commit == nil || len(st.Commit.Hash) != 40 {
		t.Fatalf("commit = %+v, want a 40-char hash", st.Commit)
	}
	if st.Commit.Message != "second" {
		t.Errorf("commit message = %q, want %q", st.Commit.Message, "second")
	}
	if st.Commit.Author != "Test" || st.Commit.Email != "test@test.invalid" {
		t.Errorf("commit author = %q <%q>, want Test <test@test.invalid>", st.Commit.Author, st.Commit.Email)
	}
	if st.Dirty || st.DirtyCount != 0 {
		t.Errorf("fresh clone should be clean, got dirty=%v count=%d", st.Dirty, st.DirtyCount)
	}
	if st.Ahead != 0 || st.Behind != 0 {
		t.Errorf("fresh clone should be level, got ahead=%d behind=%d", st.Ahead, st.Behind)
	}
}

// TestGetStatus_PackedRepoPopulatesTrackingBranch covers the second, distinct
// symptom of agent-os-r1a: getStatusCLI did not set TrackingBranch, so while the
// go-git path was dead this API field came back empty for every stack, always.
//
// It is unchanged by agent-os-yo9e, deliberately: it was the test that PROVED
// the two implementations were not interchangeable, and it is now the test that
// proves the port carried the field across. getStatusCLI resolves @{upstream}
// itself, so this passes on the surviving implementation for a better reason
// than it used to — the tracking branch is read from git rather than assumed to
// be origin/<same-name>.
func TestGetStatus_PackedRepoPopulatesTrackingBranch(t *testing.T) {
	local, remote := gitPackedRepo(t)
	s := &GitService{}

	st, err := s.GetStatus(local)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if st.TrackingBranch != "origin/main" {
		t.Errorf("trackingBranch = %q, want origin/main", st.TrackingBranch)
	}
	if st.RemoteURL != remote {
		t.Errorf("remoteURL = %q, want %q", st.RemoteURL, remote)
	}
}

// TestGetStatus_PackedRepoAheadBehind pins ahead/behind, and TrackingBranch
// alongside them, after rewinding one commit off the upstream tip.
//
// It exercised go-git's getDivergence/findMergeBase/countCommits until
// agent-os-yo9e; it now exercises getStatusCLI's `rev-list --left-right --count`
// and its @{upstream} lookup. The assertion is unchanged because the CONTRACT is
// unchanged — which is the only reason the implementation could be swapped.
func TestGetStatus_PackedRepoAheadBehind(t *testing.T) {
	local, _ := gitPackedRepo(t)
	s := &GitService{}

	cmd := exec.Command("git", "reset", "--hard", "HEAD~1")
	cmd.Dir = local
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}

	st, err := s.GetStatus(local)
	if err != nil {
		t.Fatalf("GetStatus after rewind failed: %v", err)
	}
	if st.Behind != 1 || st.Ahead != 0 {
		t.Errorf("after rewinding one commit: ahead=%d behind=%d, want 0/1", st.Ahead, st.Behind)
	}
	if st.TrackingBranch != "origin/main" {
		t.Errorf("trackingBranch = %q, want origin/main", st.TrackingBranch)
	}
}

// TestGetStatus_PackedRepoDirtyWorktree pins Dirty and DirtyCount for a single
// modified tracked file. It exercised go-git's repo.Worktree()/worktree.Status()
// until agent-os-yo9e and now exercises `git status --porcelain`.
//
// One modified tracked file is the case where the two agree. They do NOT agree
// on an untracked DIRECTORY: porcelain emits one entry for the directory where
// go-git listed every file under it, so DirtyCount is now an entry count. That
// difference is asserted, with its fixture, in git_parity_yo9e_test.go.
func TestGetStatus_PackedRepoDirtyWorktree(t *testing.T) {
	local, _ := gitPackedRepo(t)
	s := &GitService{}

	if err := os.WriteFile(filepath.Join(local, "compose.yaml"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := s.GetStatus(local)
	if err != nil {
		t.Fatalf("GetStatus on a dirty packed repo failed: %v", err)
	}
	if !st.Dirty || st.DirtyCount != 1 {
		t.Errorf("dirty tree: dirty=%v count=%d, want true/1", st.Dirty, st.DirtyCount)
	}
}

// TestGetStatus_PackedRepoWithSymlinkedSubdir pins how a symlinked subdirectory
// inside a stack is counted: as ONE untracked entry, not traversed into.
//
// The original risk was go-git-specific — v5.19.2 wrapped the worktree
// filesystem in a boundary rejecting paths whose leading directories are
// symlinks — and that risk left with the library in agent-os-yo9e. The
// ASSERTION outlives it: operators do symlink directories into a stacks tree,
// and "one entry, not traversed" is the answer `git status --porcelain` gives
// and the one the UI's dirty count depends on.
func TestGetStatus_PackedRepoWithSymlinkedSubdir(t *testing.T) {
	local, _ := gitPackedRepo(t)

	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "extra.yaml"), []byte("x: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(local, "linked")); err != nil {
		t.Skipf("symlinks unsupported on this filesystem: %v", err)
	}

	s := &GitService{}
	st, err := s.GetStatus(local)
	if err != nil {
		t.Fatalf("GetStatus failed on a repo containing a symlinked subdir: %v", err)
	}
	if !st.Dirty || st.DirtyCount != 1 {
		t.Errorf("symlinked subdir should read as one untracked entry, got dirty=%v count=%d", st.Dirty, st.DirtyCount)
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
}

// TestGetStatus_PackedRepoBehindAcrossMerge pins the divergence count to git's
// own answer across a MERGE commit, on a repo whose history is merge-per-feature
// exactly like this one.
//
// It was written against go-git's countCommits, which followed first parents only
// and reported 1 where git says 3. agent-os-yo9e deleted that walk, so the test
// now guards a different and still-live mistake: `rev-list --left-right --count`
// prints BEHIND then AHEAD, and getStatusCLI assigns parts[0] to Behind and
// parts[1] to Ahead. Swap those two and this fixture — behind 3, ahead 0 — is
// what catches it, because it is the only one in the package where the two
// counts differ.
func TestGetStatus_PackedRepoBehindAcrossMerge(t *testing.T) {
	local, _ := gitPackedRepo(t)

	run := func(dir string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.invalid",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Build a merge on top of origin/main: two commits on a side branch, then a
	// no-ff merge — so origin/main is ahead of HEAD by merge + 2 = 3 commits,
	// while the first-parent chain from the merge back to HEAD is just 1 step.
	base := run(local, "rev-parse", "HEAD")
	run(local, "checkout", "-b", "side")
	for _, msg := range []string{"side one", "side two"} {
		if err := os.WriteFile(filepath.Join(local, msg+".yaml"), []byte("# "+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(local, "add", ".")
		run(local, "commit", "-m", msg)
	}
	run(local, "checkout", "main")
	run(local, "merge", "--no-ff", "-m", "merge side", "side")
	run(local, "push", "origin", "main")
	run(local, "branch", "-D", "side")
	run(local, "reset", "--hard", base)

	wantBehind := run(local, "rev-list", "--count", "HEAD..origin/main")
	if wantBehind != "3" {
		t.Fatalf("fixture did not produce the intended shape: git says behind=%s, want 3", wantBehind)
	}

	s := &GitService{}
	st, err := s.GetStatus(local)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if got := strconv.Itoa(st.Behind); got != wantBehind {
		t.Errorf("behind = %s, git rev-list --count HEAD..origin/main says %s", got, wantBehind)
	}
	if st.Ahead != 0 {
		t.Errorf("ahead = %d, want 0", st.Ahead)
	}
}
