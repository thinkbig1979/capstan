package services

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// statusFor maps a GitService error to the HTTP status handleError would write,
// which is what a caller of the API actually observes.
func statusFor(err error) (int, string) {
	if err == nil {
		return http.StatusOK, "-"
	}
	var appErr *models.AppError
	if errors.As(err, &appErr) {
		return appErr.Status, appErr.Code
	}
	return http.StatusInternalServerError, "INTERNAL_ERROR"
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	//nolint:gosec // test helper, explicit argv, not a shell string
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v (%s)", args, dir, err, out)
	}
}

func repoWithCommit(t *testing.T, dir string) string {
	t.Helper()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	return dir
}

// TestGitEntryPoints_NotARepoIsA404 pins agent-os-pawv.
//
// A stack directory that is not a git repository produced 404 GIT_NOT_REPO from
// GET /git and a generic 500 from /git/log, /git/diff and the file-log path —
// one condition, four codes, because only the status path had the guard. The
// 500 told an operator "the server is broken"; the 404 told them "point this
// somewhere else". Only one of those was true.
//
// The two "stays 200" arms pin that those shapes keep working. They do NOT
// discriminate the probe mechanism: gitFailure runs only after a command has
// already failed, so on a shape that succeeds the probe never executes and a
// wrong probe cannot be observed. That property is the design's own defence
// against the regression — a working endpoint structurally cannot be flipped
// to 404 — and it was measured, not assumed: substituting go-git for the CLI
// probe leaves this test green.
//
// TestGitDiff_BadHashInsideRepoIsNotNotARepo below is the arm that does
// discriminate, and it is where the go-git substitution actually fails.
func TestGitEntryPoints_NotARepoIsA404(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// A directory nested inside a real repo: the git CLI walks up to the parent
	// .git, so logs work. go-git's PlainOpen does not walk up.
	nested := repoWithCommit(t, t.TempDir())
	sub := filepath.Join(nested, "stacks", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")
	src := repoWithCommit(t, t.TempDir())
	mustGit(t, src, "remote", "add", "origin", bare)
	mustGit(t, src, "push", "-q", "origin", "HEAD")

	missing := filepath.Join(t.TempDir(), "gone")

	head := func(dir string) string {
		//nolint:gosec // test helper, explicit argv, not a shell string
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			return "0000000000000000000000000000000000000000"
		}
		return string(out[:len(out)-1])
	}

	entryPoints := []struct {
		name string
		call func(dir string) error
	}{
		{"GetLog", func(d string) error { _, e := svc.GetLog(d, 50, 0); return e }},
		{"GetDiff", func(d string) error { _, e := svc.GetDiff(d, head(d)); return e }},
		{"GetLogForFile", func(d string) error { _, e := svc.GetLogForFile(d, ".", 50); return e }},
	}

	dirs := []struct {
		name string
		path string
		want int
		why  string
	}{
		{"not a repo", t.TempDir(), http.StatusNotFound,
			"the whole point of the bead"},
		{"directory is gone", missing, http.StatusNotFound,
			"no directory cannot be a repository"},
		{"subdirectory of a repo", sub, http.StatusOK,
			"REGRESSION GUARD: the git CLI walks up to the parent .git and serves logs today"},
		{"bare repo with commits", bare, http.StatusOK,
			"REGRESSION GUARD: a bare repo has commits and serves logs today"},
	}

	for _, d := range dirs {
		for _, ep := range entryPoints {
			got, code := statusFor(ep.call(d.path))
			if got != d.want {
				t.Errorf("%s / %s: got HTTP %d (%s), want %d — %s",
					d.name, ep.name, got, code, d.want, d.why)
			}
			if d.want == http.StatusNotFound && code != models.ErrGitNotRepo {
				t.Errorf("%s / %s: got code %q, want %q — a 404 that does not say why is no better than the 500 it replaced",
					d.name, ep.name, code, models.ErrGitNotRepo)
			}
		}
	}
}

// TestGitEntryPoints_RepoWithNoCommitsIsNotClaimedNotARepo is the scope boundary.
//
// A repo with no commits IS a repository, so this fix must not claim otherwise.
// Making it a 404 GIT_NOT_REPO here would turn one lying message into four —
// exactly the defect agent-os-xmtf exists to fix. Giving that case an honest
// answer belongs to xmtf; this asserts only that pawv does not pre-empt it wrongly.
func TestGitEntryPoints_RepoWithNoCommitsIsNotClaimedNotARepo(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	empty := t.TempDir()
	mustGit(t, empty, "init", "-q")

	_, err := svc.GetLog(empty, 50, 0)
	if err == nil {
		t.Skip("GetLog unexpectedly succeeded on a repo with no commits")
	}
	if _, code := statusFor(err); code == models.ErrGitNotRepo {
		t.Errorf("a repo with no commits was reported as %q; it IS a repository (see agent-os-xmtf)", code)
	}
}

// TestGitDiff_BadHashInsideRepoIsNotNotARepo discriminates the probe mechanism,
// which the table above cannot.
//
// A well-formed but unknown commit hash makes getDiffCLI's first command fail
// inside a directory that IS served by a repository — so gitFailure runs, and
// its answer is observable. The git CLI walks up to the parent .git and says
// "yes, a repository", so the failure is correctly reported as something other
// than GIT_NOT_REPO. go-git's PlainOpen does not walk up, so a go-git probe
// answers "not a repository" about a directory whose logs it serves happily,
// and turns a bad-hash request into a confident lie.
//
// handlers/git.go validates only that the hash is hex and at least 7 chars, so
// unknown-but-well-formed hashes reach here routinely.
func TestGitDiff_BadHashInsideRepoIsNotNotARepo(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	root := repoWithCommit(t, t.TempDir())
	sub := filepath.Join(root, "stacks", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Well-formed, hex, and certainly not a commit in this repo.
	const unknownHash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	if _, err := svc.GetDiff(sub, unknownHash); err != nil {
		if _, code := statusFor(err); code == models.ErrGitNotRepo {
			t.Errorf("a bad commit hash inside a repo was reported as %q; the directory is served by a repository, the hash is what is wrong", code)
		}
	} else {
		t.Fatal("precondition: GetDiff unexpectedly succeeded for an unknown hash")
	}

	// Control: the same unknown hash at the repo root, where both probes agree
	// it is a repository. Proves the arm above is about the probe walking up,
	// not about bad hashes in general.
	if _, err := svc.GetDiff(root, unknownHash); err != nil {
		if _, code := statusFor(err); code == models.ErrGitNotRepo {
			t.Errorf("control: a bad hash at a repo root was reported as %q", code)
		}
	}
}
