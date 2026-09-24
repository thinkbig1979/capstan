package services

import (
	"net/http"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestPull_BareRepoIsAClientError pins agent-os-00zg.
//
// A bare repository has no work tree to pull into, so pullCLI's first command,
// `status --porcelain`, failed with "this operation must be run in a work tree"
// and the plain error was classified as a 500. It is a request the repository
// cannot satisfy, not a server fault.
func TestPull_BareRepoIsAClientError(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")
	src := repoWithCommit(t, t.TempDir())
	mustGit(t, src, "remote", "add", "origin", bare)
	mustGit(t, src, "push", "-q", "origin", "HEAD")

	_, err := svc.Pull(bare)
	if got, code := statusFor(err); got != http.StatusConflict || code != models.ErrGitBareRepo {
		t.Fatalf("Pull(bare) = HTTP %d (%s): %v, want 409 %s", got, code, err, models.ErrGitBareRepo)
	}

	// Control on the same instrument: a clean clone with a work tree pulls
	// (up to date), so a guard that refused every repository fails here.
	clone := t.TempDir()
	mustGit(t, clone, "clone", "-q", bare, ".")
	if _, err := svc.Pull(clone); err != nil {
		got, code := statusFor(err)
		t.Fatalf("Pull(clone of bare) = HTTP %d (%s): %v, want success", got, code, err)
	}
}
