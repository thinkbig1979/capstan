package services

import (
	"net/http"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestGetStatus_BareRepoWithCommits pins agent-os-m2g8.
//
// The scanner reports a bare repository as a git repo and the log/diff
// endpoints serve it, but GetStatus answered 500 because `git status` needs a
// work tree: "fatal: this operation must be run in a work tree". A bare repo
// has a branch and a commit and nothing else status can say, so it answers
// those and marks itself bare.
func TestGetStatus_BareRepoWithCommits(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")
	src := repoWithCommit(t, t.TempDir())
	mustGit(t, src, "remote", "add", "origin", bare)
	mustGit(t, src, "push", "-q", "origin", "HEAD")
	wantHash := gitOut(t, src, "rev-parse", "HEAD")
	wantBranch := gitOut(t, src, "rev-parse", "--abbrev-ref", "HEAD")

	st, err := svc.GetStatus(bare)
	if got, code := statusFor(err); got != http.StatusOK {
		t.Fatalf("GetStatus(bare) = HTTP %d (%s): %v", got, code, err)
	}
	if !st.IsBare {
		t.Errorf("IsBare = false for a bare repository")
	}
	if st.Branch != wantBranch {
		t.Errorf("Branch = %q, want %q", st.Branch, wantBranch)
	}
	if st.Commit.Hash != wantHash || st.Commit.Short != wantHash[:7] || st.Commit.Message != "seed" {
		t.Errorf("Commit = %+v, want hash %s, message %q", st.Commit, wantHash, "seed")
	}

	// Control on the same instrument: a repository with a work tree is not
	// bare, so a detector that answered true for everything fails here.
	st, err = svc.GetStatus(src)
	if err != nil {
		t.Fatalf("GetStatus(non-bare) = %v", err)
	}
	if st.IsBare {
		t.Errorf("IsBare = true for a repository with a work tree")
	}
}

// TestGetStatus_EmptyBareRepoHasNoCommits is the empty half of agent-os-m2g8.
//
// In a bare repository with an unborn HEAD, `rev-parse --abbrev-ref HEAD` and
// `rev-parse HEAD` both exit 0 and print the literal "HEAD" (OBSERVED, git
// 2.47.3): with no work tree git cannot treat it as a path, so it echoes the
// argument. Only the `status` call's 500 used to stop that reaching the
// response as branch "HEAD", commit "HEAD". It answers what a non-bare `git
// init` answers.
func TestGetStatus_EmptyBareRepoHasNoCommits(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")

	_, err := svc.GetStatus(bare)
	if got, code := statusFor(err); got != http.StatusNotFound || code != models.ErrGitNoCommits {
		t.Fatalf("GetStatus(empty bare) = HTTP %d (%s): %v, want 404 %s", got, code, err, models.ErrGitNoCommits)
	}
}
