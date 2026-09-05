package services

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// fixtureBranch is the branch name every step of pullFixture states outright.
const fixtureBranch = "main"

// pullFixture builds a real, entirely offline upstream/clone pair and returns
// the working clone plus the seed clone that can advance the upstream. No
// network: the "remote" is a bare repository on disk, so the unreachable arm is
// produced by pointing origin at a path that does not exist rather than by
// waiting for DNS to fail.
// fixtureBranch is named explicitly at every step below instead of being left
// to git's default, and that is the whole point of it.
//
// `git init` takes the branch name from init.defaultBranch, which is AMBIENT
// USER CONFIGURATION. A box that sets it to "main" and a box that does not
// build different fixtures out of identical code. That is not hypothetical:
// this file passed locally and went red in CI for exactly that reason, and the
// local pass was the accident. On a box with no init.defaultBranch the seed
// repo and the bare upstream are both born on "master", the push creates
// "main" on the upstream while its HEAD still points at the absent "master",
// and the clone below then checks nothing out.
func pullFixture(t *testing.T) (work, seed, root string) {
	t.Helper()
	root = t.TempDir()

	seed = filepath.Join(root, "seed")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatalf("mkdir seed: %v", err)
	}
	mustGit(t, seed, "init", "-q", "-b", fixtureBranch)
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "seed")

	upstream := filepath.Join(root, "upstream.git")
	mustGit(t, root, "init", "-q", "--bare", "-b", fixtureBranch, "upstream.git")
	mustGit(t, seed, "push", "-q", upstream, "HEAD:refs/heads/"+fixtureBranch)

	work = filepath.Join(root, "work")
	// -b pins the checked-out branch to the one just pushed rather than taking
	// it from the remote's HEAD, so the clone does not depend on what that
	// HEAD happens to point at either.
	mustGit(t, root, "clone", "-q", "-b", fixtureBranch, upstream, "work")

	// PRECONDITIONS, ASSERTED RATHER THAN ASSUMED. These three lines are the
	// most important thing in this file.
	//
	// `git clone` EXITS 0 when it cannot check anything out. It prints
	// "warning: remote HEAD refers to nonexistent ref, unable to checkout" and
	// returns success, so mustGit cannot see the failure. OBSERVED, git 2.47.3
	// with the global config ignored: clone exit 0, zero files in the work
	// tree, `rev-parse HEAD` fatal.
	//
	// What that cost, MEASURED on the pre-fix commit in that same environment:
	// the divergence arm failed as a MISCLASSIFICATION ("got INTERNAL_ERROR,
	// want GIT_CONFLICT"), which reads as a bug in pullFailure and is not one.
	// Worse, the OTHER TWO ARMS PASSED, and passed VACUOUSLY -- they assert
	// "not GIT_CONFLICT", and this is the error they were actually asserting
	// against:
	//
	//	ARM 1 actual error: failed to get HEAD: git rev-parse: exit status 128
	//	  (fatal: ambiguous argument 'HEAD': unknown revision or path ...
	//
	// That is pullCLI bailing at its own `rev-parse HEAD`, BEFORE pullFailure
	// is ever called. Two green arms, and the code under test never ran. An
	// assertion satisfied by more than one path, only one of which is the
	// subject, is not a test -- and green is exactly when nobody looks.
	//
	// So the upstream check names the branch it expects rather than merely
	// resolving something: "it tracks an upstream" is true of the broken
	// fixture too, once HEAD exists.
	mustGit(t, work, "rev-parse", "--verify", "HEAD")
	if got := gitOut(t, work, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/"+fixtureBranch {
		t.Fatalf("fixture precondition: work tracks %q, want %q. The clone did not set up the "+
			"branch this fixture depends on, so every arm below would be testing the wrong repository.",
			got, "origin/"+fixtureBranch)
	}

	return work, seed, root
}

// gitOut is mustGit for the cases that need the command's output, not just its
// exit status.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // test helper, explicit argv, not a shell string
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v (%s)", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// advanceUpstream puts one more commit on the upstream's main branch.
func advanceUpstream(t *testing.T, seed, root string) {
	t.Helper()
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "remote advances")
	mustGit(t, seed, "push", "-q", filepath.Join(root, "upstream.git"), "HEAD:refs/heads/"+fixtureBranch)
}

// TestPullCLI_DiscriminatesPullFailures pins agent-os-fv2j.
//
// pullCLI answered EVERY non-nil error from `git pull --ff-only` with a flat
// 500 GIT_CONFLICT. An expired token, a DNS failure, a deleted remote and a
// genuinely diverged branch all told the operator they had a merge conflict.
// Only the last of those is one, and an operator reading GIT_CONFLICT goes
// looking for a divergence that is not there.
//
// The two arms run on the SAME instrument and must come out DIFFERENTLY, which
// is the whole assertion. A fix that relabels every failure with a friendlier
// code discriminates nothing and is worse than the bug, because it also hides
// the real conflict; the control arm is what forbids that.
//
// MEASURED, git 2.47.3, six real failure shapes driven through `git pull
// --ff-only` directly. The exit status alone does not separate them (a
// divergence exits 128 while auth, DNS, a missing remote and a missing upstream
// all exit 1), so the classification asks git three follow-up questions instead
// — see pullFailure:
//
//	                         ls-remote  rev-parse @{u}  is-ancestor HEAD @{u}
//	diverged, non-ff             0            0                1
//	remote path missing        128            0                0
//	no remote configured       128            1              128
//	DNS failure                128            0                0
//	auth failure               128            0                0
//	up to date                   0            0                0
//	reachable, no upstream       0            1              128
//
// The last row is why the upstream probe is not redundant: without it
// `is-ancestor` exits 128 on a branch that has no upstream at all, and that
// failure would be reported as a divergence.
func TestPullCLI_DiscriminatesPullFailures(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// ARM 1 — THE DEFECT. A remote that cannot be read is not a conflict.
	t.Run("unreachable remote is not a conflict", func(t *testing.T) {
		work, _, root := pullFixture(t)
		mustGit(t, work, "remote", "set-url", "origin", filepath.Join(root, "gone.git"))

		_, err := svc.pullCLI(work)
		if err == nil {
			t.Fatal("precondition: pullCLI unexpectedly succeeded against a remote that does not exist")
		}
		status, code := statusFor(err)
		if code == models.ErrGitConflict {
			t.Errorf("unreachable remote: got code %q — nothing about this repository has diverged; "+
				"the operator needs the remote or the credential fixed, not a merge", code)
		}
		if status == http.StatusInternalServerError && code == models.ErrGitConflict {
			t.Errorf("unreachable remote: got HTTP %d (%s); a 500 tells the operator Capstan broke "+
				"when the truth is the remote could not be read", status, code)
		}
	})

	// ARM 2 — THE CONTROL, and the load-bearing half. A genuine
	// non-fast-forward divergence must STILL be reported as a conflict, on
	// this same call.
	t.Run("genuine divergence is still a conflict", func(t *testing.T) {
		work, seed, root := pullFixture(t)
		advanceUpstream(t, seed, root)
		mustGit(t, work, "-c", "user.email=t@t", "-c", "user.name=t",
			"commit", "-q", "--allow-empty", "-m", "local diverges")

		_, err := svc.pullCLI(work)
		if err == nil {
			t.Fatal("precondition: pullCLI unexpectedly succeeded on a diverged branch")
		}
		status, code := statusFor(err)
		if code != models.ErrGitConflict {
			t.Errorf("CONTROL, diverged branch: got code %q, want %q — this one really is a "+
				"conflict, and a fix that stops saying so has discriminated nothing",
				code, models.ErrGitConflict)
		}
		if status != http.StatusConflict {
			t.Errorf("CONTROL, diverged branch: got HTTP %d, want %d", status, http.StatusConflict)
		}
	})

	// ARM 3 — the guard on the upstream probe. A reachable remote on a branch
	// with no upstream is a configuration problem, not a divergence. Without
	// the upstream probe, `merge-base --is-ancestor` exits 128 here and this
	// arm reports a conflict.
	t.Run("reachable remote with no upstream is not a conflict", func(t *testing.T) {
		work, _, _ := pullFixture(t)
		mustGit(t, work, "checkout", "-q", "-b", "no-upstream")

		_, err := svc.pullCLI(work)
		if err == nil {
			t.Fatal("precondition: pullCLI unexpectedly succeeded on a branch with no upstream")
		}
		if _, code := statusFor(err); code == models.ErrGitConflict {
			t.Errorf("branch with no upstream: got code %q — the branch tracks nothing, so there is "+
				"nothing it could have diverged from", code)
		}
	})
}
