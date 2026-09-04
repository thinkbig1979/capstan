package services

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// notRepoMessage is the exact string getStatusCLI used to return for every
// failure of `rev-parse --abbrev-ref HEAD`, regardless of cause.
const notRepoMessage = "Not a git repository"

// TestGetStatusCLI_DiscriminatesEmptyRepoFromNonRepo pins agent-os-xmtf.
//
// getStatusCLI answered ANY failure of `rev-parse --abbrev-ref HEAD` with a
// flat 404 "Not a git repository". A repository with no commits is a normal
// state, not a missing repository, and the two are fixed differently: an empty
// repo needs a commit or a remote, an absent one needs the stack pointed
// somewhere else. The old message sent both operators to the second.
//
// The two arms run on the SAME instrument and must come out DIFFERENTLY —
// that is the whole assertion. An implementation that returns a friendlier
// message for everything passes the empty-repo arm and fails the control, and
// is worse than the bug it replaces because it hides the real non-repo case.
//
// The arms call getStatusCLI DIRECTLY rather than through GetStatus, and that
// is deliberate. MEASURED on this tree: GetStatus (git.go:60) returns a typed
// *models.AppError from getStatusGoGit as-is without falling back to the CLI,
// and openRepo already mints a 404 GIT_NOT_REPO for a non-repo and for a
// missing directory. So through GetStatus the control never executes this
// guard at all — it would be satisfied by openRepo and could only come out one
// way. Only the empty-repo case reaches getStatusCLI in production, via
// go-git's non-typed "failed to get HEAD: reference not found".
func TestGetStatusCLI_DiscriminatesEmptyRepoFromNonRepo(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	empty := t.TempDir()
	mustGit(t, empty, "init", "-q")

	// ARM 1 — the defect. A repository with no commits.
	_, err := svc.getStatusCLI(empty)
	if err == nil {
		t.Fatal("precondition: getStatusCLI unexpectedly succeeded on a repo with no commits")
	}
	if _, code := statusFor(err); code == models.ErrGitNotRepo {
		t.Errorf("empty repo: got code %q — it IS a repository, `git rev-parse --git-dir` says so; "+
			"the operator needs a commit or a remote, not a different directory", code)
	}
	if strings.Contains(err.Error(), notRepoMessage) {
		t.Errorf("empty repo: message %q still claims the directory is not a repository", err.Error())
	}

	// ARM 2 — the CONTROL, and the load-bearing half. A genuinely non-repo
	// directory must STILL be diagnosed as one, on this same call.
	notRepo := t.TempDir()
	_, err = svc.getStatusCLI(notRepo)
	if err == nil {
		t.Fatal("precondition: getStatusCLI unexpectedly succeeded on a non-repo directory")
	}
	status, code := statusFor(err)
	if status != http.StatusNotFound || code != models.ErrGitNotRepo {
		t.Errorf("CONTROL, not a repo: got HTTP %d (%s), want 404 (%s) — a fix that gives every "+
			"failure a nicer message discriminates nothing and is worse than the bug",
			status, code, models.ErrGitNotRepo)
	}

	// ARM 3 — a missing directory cannot be a repository either.
	missing := filepath.Join(t.TempDir(), "gone")
	if _, err := svc.getStatusCLI(missing); err != nil {
		if status, code := statusFor(err); status != http.StatusNotFound || code != models.ErrGitNotRepo {
			t.Errorf("CONTROL, directory is gone: got HTTP %d (%s), want 404 (%s)",
				status, code, models.ErrGitNotRepo)
		}
	} else {
		t.Error("precondition: getStatusCLI unexpectedly succeeded on a missing directory")
	}

	// ARM 4 — REGRESSION GUARD. A repository with commits still answers 200,
	// so arms 1-3 are shown to be about diagnosis and not about having broken
	// the working path.
	good := repoWithCommit(t, t.TempDir())
	result, err := svc.getStatusCLI(good)
	if err != nil {
		t.Fatalf("regression: getStatusCLI failed on a healthy repo: %v", err)
	}
	if result.Commit == nil || result.Commit.Hash == "" {
		t.Errorf("regression: healthy repo returned no commit: %+v", result)
	}
}

// TestGetStatus_EmptyRepoIsNotClaimedNotARepo is the operator-visible half:
// the same defect through the public entry point the HTTP handler calls.
//
// MEASURED: go-git fails an empty repo with "failed to get HEAD: reference not
// found", which is not a *models.AppError, so GetStatus falls through to
// getStatusCLI — this is the one condition that actually reaches the guard in
// production, which is why the empty repo was the case that got lied to.
func TestGetStatus_EmptyRepoIsNotClaimedNotARepo(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	empty := t.TempDir()
	mustGit(t, empty, "init", "-q")

	_, err := svc.GetStatus(empty)
	if err == nil {
		t.Skip("GetStatus unexpectedly succeeded on a repo with no commits")
	}
	if _, code := statusFor(err); code == models.ErrGitNotRepo {
		t.Errorf("GET /git on a repo with no commits reports %q; the repository exists and is empty", code)
	}
	if strings.Contains(err.Error(), notRepoMessage) {
		t.Errorf("GET /git on a repo with no commits says %q", err.Error())
	}

	// CONTROL on the same entry point: a non-repo still reports GIT_NOT_REPO.
	notRepo := t.TempDir()
	if _, err := svc.GetStatus(notRepo); err != nil {
		if status, code := statusFor(err); status != http.StatusNotFound || code != models.ErrGitNotRepo {
			t.Errorf("CONTROL: GET /git on a non-repo got HTTP %d (%s), want 404 (%s)",
				status, code, models.ErrGitNotRepo)
		}
	} else {
		t.Error("precondition: GetStatus unexpectedly succeeded on a non-repo directory")
	}
}

// NO RUNTIME LOCALE ARM EXISTS HERE, DELIBERATELY, AND THIS IS THE EVIDENCE.
//
// The hard rule of agent-os-xmtf is that the empty-repo/non-repo split must
// come from git's exit codes, never from its prose, because git translates its
// messages (19 catalogs are installed on this host; MEASURED,
// `(unset LC_ALL; LANGUAGE=de git rev-parse --git-dir)` prints
// "Schwerwiegend: Kein Git-Repository ..."). The obvious way to pin that rule
// would be a test that forces a German child locale and asserts the split still
// lands.
//
// That test CANNOT FAIL, so it is not written. gitCmdWithCreds
// (git_credentials.go:242, agent-os-vq3p) APPENDS "LC_ALL=C", "LANGUAGE=" to
// the child environment, after everything the test could set — so t.Setenv is
// overridden and the child speaks English no matter what. A text-classifying
// implementation would pass such a test in every environment reachable from
// here. Writing it would produce a green arm that proves nothing, which is
// worse than not having it: it would read as coverage of the one rule that
// most needs it.
//
// Defeating the pin would mean editing git_credentials.go, which belongs to
// agent-os-vq3p and is out of scope. The rule is therefore enforced by
// construction instead — getStatusCLI's classification performs no string
// comparison against git output at all — and checked by reading the diff.
