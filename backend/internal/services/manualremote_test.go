//go:build manualremote

// Throwaway verification harness for agent-os-vkr (go-git v5.17.1 -> v5.19.2).
// Excluded from every normal build by the `manualremote` tag. Run with:
//
//	go test -tags=manualremote -count=1 -v ./internal/services/ -run TestManualRemote
//
// Nothing in the unit suite exercises a real remote, so a green run says
// nothing about whether stack git operations still work against one. This
// drives GitService against github.com/thinkbig1979/capstan over BOTH an
// HTTPS and an SSH remote.
//
// Since agent-os-r1a was fixed this DOES exercise go-git meaningfully: the
// nil-cache panic no longer fires, so repo.Worktree, worktree.Status,
// getDivergence, findMergeBase, countCommits and mapCommit all run. Before
// that fix everything below step 2a was served by the git CLI and this file
// was near-worthless as evidence about a go-git upgrade.
//
// Pull remains CLI by construction (git.go: Pull -> pullCLI); that is the
// product design, not a fallback.
//
// The packfile regression tests in git_packfile_test.go cover the same ground
// hermetically and run in the normal suite. Keep this one for what it alone
// does: a real HTTPS and SSH remote, over the network.
package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	httpsRemote = "https://github.com/thinkbig1979/capstan.git"
	sshRemote   = "git@github.com:thinkbig1979/capstan.git"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestManualRemote(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{"HTTPS", httpsRemote},
		{"SSH", sshRemote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			local := filepath.Join(base, "repo")

			// 1. CLONE. Capstan has no clone path of its own (grep confirms no
			// clone call site outside test scaffolding), so this is the plain
			// CLI clone an operator performs before pointing capstan at a dir.
			runGit(t, base, "clone", "--branch", "main", tc.url, local)
			t.Logf("cloned %s -> %s", tc.url, local)

			s := &GitService{}

			// 2a. go-git directly: openRepo is osfs.New +
			// filesystem.NewStorage + git.Open, repo.Head() is the reference
			// lookup. Both must work against a real clone.
			repo, err := s.openRepo(local)
			if err != nil {
				t.Fatalf("openRepo (go-git) failed on a real clone: %v", err)
			}
			headRef, err := repo.Head()
			if err != nil {
				t.Fatalf("repo.Head (go-git) failed on a real clone: %v", err)
			}
			if want := runGit(t, local, "rev-parse", "HEAD"); headRef.Hash().String() != want {
				t.Errorf("go-git Head = %s, git CLI says %s", headRef.Hash(), want)
			}
			if headRef.Name().Short() != "main" {
				t.Errorf("go-git Head ref = %q, want main", headRef.Name().Short())
			}
			t.Logf("go-git reached: openRepo + Head OK at %s", headRef.Hash())

			// The full go-git status path must succeed against a real clone.
			// Asserted, not logged: GetStatus below would silently fall back to
			// the CLI and hide a regression here, which is exactly how
			// agent-os-r1a survived in production.
			if _, err := s.getStatusGoGit(local); err != nil {
				t.Fatalf("getStatusGoGit failed on a real clone: %v", err)
			}

			// 2b. STATUS through the public API — what capstan actually serves.
			st, err := s.GetStatus(local)
			if err != nil {
				t.Fatalf("GetStatus failed: %v", err)
			}
			if st.Branch != "main" {
				t.Errorf("branch = %q, want main", st.Branch)
			}
			if st.Commit == nil || len(st.Commit.Hash) != 40 {
				t.Fatalf("commit = %+v, want a 40-char hash", st.Commit)
			}
			if st.Commit.Author == "" || st.Commit.Message == "" || st.Commit.Date == "" {
				t.Errorf("commit metadata incomplete: %+v", st.Commit)
			}
			if st.RemoteURL != tc.url {
				t.Errorf("remoteURL = %q, want %q", st.RemoteURL, tc.url)
			}
			// Populated by the go-git path only; getStatusCLI never sets it.
			// This assertion therefore also proves GetStatus did not fall back.
			if st.TrackingBranch != "origin/main" {
				t.Errorf("trackingBranch = %q, want origin/main", st.TrackingBranch)
			}
			if st.Dirty || st.DirtyCount != 0 || st.Ahead != 0 || st.Behind != 0 {
				t.Errorf("fresh clone should be clean and level: %+v", st)
			}
			t.Logf("status: branch=%s commit=%s author=%q remote=%s",
				st.Branch, st.Commit.Short, st.Commit.Author, st.RemoteURL)

			// 3. STATUS reports a dirty worktree, via go-git's repo.Worktree()
			// and worktree.Status().
			if err := os.WriteFile(filepath.Join(local, "vkr-scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			dirtySt, err := s.GetStatus(local)
			if err != nil {
				t.Fatalf("GetStatus on dirty tree failed: %v", err)
			}
			if !dirtySt.Dirty || dirtySt.DirtyCount != 1 {
				t.Errorf("dirty tree: dirty=%v count=%d, want true/1", dirtySt.Dirty, dirtySt.DirtyCount)
			}

			// 4. PULL refuses to run on a dirty worktree.
			if _, err := s.Pull(local); err == nil {
				t.Error("Pull on a dirty worktree should fail, got nil")
			} else {
				t.Logf("pull on dirty tree correctly rejected: %v", err)
			}
			if err := os.Remove(filepath.Join(local, "vkr-scratch.txt")); err != nil {
				t.Fatal(err)
			}

			// 5. STATUS reports behind/ahead after rewinding one commit.
			head := runGit(t, local, "rev-parse", "HEAD")
			runGit(t, local, "reset", "--hard", "HEAD~1")
			behindSt, err := s.GetStatus(local)
			if err != nil {
				t.Fatalf("GetStatus after rewind failed: %v", err)
			}
			// Derive the expected count rather than hardcoding it: this clones
			// a live moving remote, so the repo shape is not under the test's
			// control. This assertion is what caught countCommits following
			// first parents only — it reported behind=1 where git said 3.
			wantBehind := runGit(t, local, "rev-list", "--count", "HEAD..origin/main")
			if got := strconv.Itoa(behindSt.Behind); got != wantBehind {
				t.Errorf("after rewind: behind=%s, git rev-list says %s", got, wantBehind)
			}
			if behindSt.Ahead != 0 {
				t.Errorf("after rewind: ahead=%d, want 0", behindSt.Ahead)
			}
			t.Logf("status after rewind: ahead=%d behind=%d", behindSt.Ahead, behindSt.Behind)

			// 6. PULL fast-forwards back to the remote tip against a real remote.
			pr, err := s.Pull(local)
			if err != nil {
				t.Fatalf("Pull failed: %v", err)
			}
			if pr.CurrentCommit != head {
				t.Errorf("pull landed on %s, want %s", pr.CurrentCommit, head)
			}
			if pr.PreviousCommit == pr.CurrentCommit {
				t.Error("pull reported no movement, expected a fast-forward")
			}
			if len(pr.ChangedFiles) == 0 {
				t.Error("pull reported no changed files across a real commit")
			}
			t.Logf("pull: %s -> %s (%d changed files)",
				pr.PreviousCommit[:7], pr.CurrentCommit[:7], len(pr.ChangedFiles))

			// 7. PULL is a no-op when already at the tip.
			pr2, err := s.Pull(local)
			if err != nil {
				t.Fatalf("second Pull failed: %v", err)
			}
			if pr2.PreviousCommit != pr2.CurrentCommit || len(pr2.ChangedFiles) != 0 {
				t.Errorf("up-to-date pull should be a no-op, got %+v", pr2)
			}

			// 8. Final status is clean and level again.
			finalSt, err := s.GetStatus(local)
			if err != nil {
				t.Fatalf("final GetStatus failed: %v", err)
			}
			if finalSt.Dirty || finalSt.Ahead != 0 || finalSt.Behind != 0 || finalSt.Commit.Hash != head {
				t.Errorf("final status not clean/level at %s: %+v", head, finalSt)
			}
		})
	}
}
