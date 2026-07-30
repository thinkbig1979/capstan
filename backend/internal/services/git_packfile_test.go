package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The go-git status path is only reachable for repositories whose objects live
// in PACKFILES. Repositories built commit-by-commit — which is what every other
// fixture in this package does — store loose objects, and the packfile reader is
// the only thing that touches the object cache. That is why agent-os-r1a
// survived 657 tests: nothing here had ever opened a packed repository.
//
// gitPackedRepo builds a bare "remote", clones it, and runs `git gc --aggressive`
// on the clone so every object is packed. No network.
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

// TestGetStatusGoGit_PackedRepo is the regression test for agent-os-r1a.
// openRepo passed a nil cache to filesystem.NewStorage, so ObjectStorage
// dereferenced it as soon as an object had to be read out of a packfile and
// getStatusGoGit panicked. GetStatus recovered and silently fell back to the
// CLI, so nothing surfaced.
//
// This asserts on getStatusGoGit directly, NOT GetStatus — the fallback would
// mask the failure and the test would pass against the broken code.
func TestGetStatusGoGit_PackedRepo(t *testing.T) {
	local, _ := gitPackedRepo(t)
	s := &GitService{}

	st, err := s.getStatusGoGit(local)
	if err != nil {
		t.Fatalf("getStatusGoGit failed on a packed repository: %v", err)
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
// symptom. getStatusCLI never sets TrackingBranch, so while the go-git path was
// dead this API field came back empty for every stack, always. Asserting it
// through the public GetStatus is deliberate: it is the contract the handler
// serves, and it fails if the fallback is silently reinstated.
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

// TestGetStatusGoGit_PackedRepoAheadBehind exercises getDivergence, findMergeBase
// and countCommits against packed objects. Every one of those was unreachable
// while the nil cache stood, so none of them had ever run on a packed repository.
func TestGetStatusGoGit_PackedRepoAheadBehind(t *testing.T) {
	local, _ := gitPackedRepo(t)
	s := &GitService{}

	cmd := exec.Command("git", "reset", "--hard", "HEAD~1")
	cmd.Dir = local
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}

	st, err := s.getStatusGoGit(local)
	if err != nil {
		t.Fatalf("getStatusGoGit after rewind failed: %v", err)
	}
	if st.Behind != 1 || st.Ahead != 0 {
		t.Errorf("after rewinding one commit: ahead=%d behind=%d, want 0/1", st.Ahead, st.Behind)
	}
	if st.TrackingBranch != "origin/main" {
		t.Errorf("trackingBranch = %q, want origin/main", st.TrackingBranch)
	}
}

// TestGetStatusGoGit_PackedRepoDirtyWorktree exercises repo.Worktree() and
// worktree.Status() against packed objects. repo.Worktree() is the call site
// that go-git v5.19.2 changed most — it now wraps the filesystem in a
// symlink-rejecting boundary — and it had never executed in this codebase.
func TestGetStatusGoGit_PackedRepoDirtyWorktree(t *testing.T) {
	local, _ := gitPackedRepo(t)
	s := &GitService{}

	if err := os.WriteFile(filepath.Join(local, "compose.yaml"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := s.getStatusGoGit(local)
	if err != nil {
		t.Fatalf("getStatusGoGit on a dirty packed repo failed: %v", err)
	}
	if !st.Dirty || st.DirtyCount != 1 {
		t.Errorf("dirty tree: dirty=%v count=%d, want true/1", st.Dirty, st.DirtyCount)
	}
}

// TestGetStatusGoGit_PackedRepoWithSymlinkedSubdir locks in behaviour that was
// specifically at risk. go-git v5.19.2 wraps the worktree filesystem in a
// boundary that rejects paths whose leading directories exist on disk as
// symlinks (v5.19.2/worktree_fs.go), and repo.Worktree() had never executed in
// this codebase before the nil-cache fix — so a stacks directory containing
// symlinked subdirectories was the most plausible way for that change to
// surface as a regression.
//
// It does not: Status reports a top-level symlink as an untracked entry rather
// than traversing into it. Keeping the test so that a future go-git bump which
// does break it fails here instead of in production.
func TestGetStatusGoGit_PackedRepoWithSymlinkedSubdir(t *testing.T) {
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
	st, err := s.getStatusGoGit(local)
	if err != nil {
		t.Fatalf("getStatusGoGit failed on a repo containing a symlinked subdir: %v", err)
	}
	if !st.Dirty || st.DirtyCount != 1 {
		t.Errorf("symlinked subdir should read as one untracked entry, got dirty=%v count=%d", st.Dirty, st.DirtyCount)
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
}

// TestGetStatusGoGit_PackedRepoBehindAcrossMerge pins the go-git divergence
// count to git's own answer across a MERGE commit.
//
// countCommits used to follow first parents only, so a merge that brought in two
// commits counted as 1 instead of 3. It was invisible while agent-os-r1a kept
// this path unreachable, and switching the path back on would have silently
// downgraded the API's behind count — getStatusCLI uses
// `git rev-list --left-right --count`, which counts correctly.
//
// This repo's own history is merge-per-feature, so the wrong answer would have
// shipped on the first real stack.
func TestGetStatusGoGit_PackedRepoBehindAcrossMerge(t *testing.T) {
	local, _ := gitPackedRepo(t)

	run := func(dir string, args ...string) string {
		t.Helper()
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
	st, err := s.getStatusGoGit(local)
	if err != nil {
		t.Fatalf("getStatusGoGit failed: %v", err)
	}
	if got := strconv.Itoa(st.Behind); got != wantBehind {
		t.Errorf("behind = %s, git rev-list --count HEAD..origin/main says %s", got, wantBehind)
	}
	if st.Ahead != 0 {
		t.Errorf("ahead = %d, want 0", st.Ahead)
	}
}
