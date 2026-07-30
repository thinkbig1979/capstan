//go:build manualremote

// Throwaway verification harness for agent-os-vkr (go-git v5.17.1 -> v5.19.2).
// Excluded from every normal build by the `manualremote` tag. Run with:
//
//	go test -tags=manualremote -count=1 -v ./internal/services/ -run TestManualRemote
//
// Nothing in the 657-test unit suite exercises a real remote, so a green run
// says nothing about whether the go-git bump broke stack git operations. This
// drives GitService against github.com/thinkbig1979/capstan over BOTH an HTTPS
// and an SSH remote.
package services

import (
	"os"
	"os/exec"
	"path/filepath"
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

			// 2a. Record which internal path GetStatus resolves to. On a real
			// clone the go-git path panics inside filesystem.ObjectStorage
			// because openRepo passes a nil cache to filesystem.NewStorage —
			// PRE-EXISTING, reproduces identically on main @ go-git v5.17.1,
			// tracked separately. Every object in a fresh clone lives in a
			// packfile, and the packfile reader is the only thing that touches
			// the cache, which is why locally-built test repos never hit it.
			if _, err := s.getStatusGoGit(local); err != nil {
				t.Logf("NOTE: go-git path unavailable, GetStatus falls back to CLI: %v", err)
			} else {
				t.Logf("NOTE: go-git path succeeded")
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
			// trackingBranch comes back empty because getStatusCLI never
			// populates it — a second consequence of the go-git path being
			// dead. Same before and after the bump; logged, not asserted.
			t.Logf("trackingBranch=%q (empty on the CLI fallback path)", st.TrackingBranch)
			if st.Dirty || st.DirtyCount != 0 || st.Ahead != 0 || st.Behind != 0 {
				t.Errorf("fresh clone should be clean and level: %+v", st)
			}
			t.Logf("status: branch=%s commit=%s author=%q remote=%s",
				st.Branch, st.Commit.Short, st.Commit.Author, st.RemoteURL)

			// 3. STATUS reports a dirty worktree (go-git worktree.Status).
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
			// Rewinding one first-parent step off a merge commit puts the
			// clone >=1 commits behind (the merge plus what it merged), so
			// assert the direction, not an exact count.
			if behindSt.Behind < 1 || behindSt.Ahead != 0 {
				t.Errorf("after rewind: ahead=%d behind=%d, want ahead=0 behind>=1", behindSt.Ahead, behindSt.Behind)
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
