package services

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// errProbeTrigger stands in for the caller's original failed git command. Every
// gitFailure call site passes the error that already failed; gitFailure only
// needs it to be non-nil before it runs its own probe.
var errProbeTrigger = errors.New("the caller's git command failed")

// TestGitFailure_SeparatesNoRepoFromProbeFailure pins BOTH directions of the
// agent-os-prfj split on ONE instrument, which is the only way the result
// discriminates.
//
// gitFailure used to branch on `probeErr != nil` and nothing finer, so three
// unrelated conditions collapsed into one 404 GIT_NOT_REPO: a directory that
// genuinely is not a repository, a stacks directory that is not mounted, and
// an image with no git binary. The first is a routine, expected answer and is
// marked as such (handlers/respond.go routineErrorCodes), which drops it to
// INFO. The other two are server faults, and marking them routine would take
// three real operator incidents off the log entirely.
//
// So "the non-git directory answers GIT_NOT_REPO" is not the assertion that
// matters — it passed before this change too. The assertion that matters is
// that the other two rows STOP answering GIT_NOT_REPO while that first row
// keeps answering it, in the same run.
func TestGitFailure_SeparatesNoRepoFromProbeFailure(t *testing.T) {
	s := &GitService{}

	t.Run("genuine non-git directory stays a typed routine 404", func(t *testing.T) {
		got := s.gitFailure(t.TempDir(), errProbeTrigger)

		var appErr *models.AppError
		if !errors.As(got, &appErr) {
			t.Fatalf("want *models.AppError, got %T: %v", got, got)
		}
		if appErr.Status != 404 || appErr.Code != models.ErrGitNotRepo {
			t.Fatalf("want 404/%s, got %d/%s", models.ErrGitNotRepo, appErr.Status, appErr.Code)
		}
	})

	t.Run("missing directory gets its own 404 code, NOT GIT_NOT_REPO", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "not-mounted")

		got := s.gitFailure(missing, errProbeTrigger)
		if got == nil {
			t.Fatal("want an error for a directory that cannot be entered, got nil")
		}

		var appErr *models.AppError
		if !errors.As(got, &appErr) {
			t.Fatalf("want *models.AppError, got %T: %v", got, got)
		}
		if appErr.Code == models.ErrGitNotRepo {
			t.Fatalf("a missing directory must NOT be reported as GIT_NOT_REPO: %v", got)
		}
		if appErr.Code != models.ErrStackDirMissing {
			t.Fatalf("want code %q, got %q", models.ErrStackDirMissing, appErr.Code)
		}
		// 404, not 5xx: agent-os-pawv's contract is that this answer is a 404
		// that says why, and B keeps that. Only the code moved.
		if appErr.Status != 404 {
			t.Fatalf("want status 404 (agent-os-pawv's contract, unchanged), got %d", appErr.Status)
		}
		// No Cause: logServerFault is silent below 500, so a cause attached
		// here would never be read, and models/errors.go asks that causes be
		// attached at the HTTP boundary rather than inside a service. The
		// WARN line's code and path are the diagnosis.
		if appErr.Cause != nil {
			t.Fatalf("no cause should be attached to a 404 that never reaches logServerFault, got %v", appErr.Cause)
		}
	})

	t.Run("missing git binary is a probe failure, NOT a missing repository", func(t *testing.T) {
		// exec.Command resolves the binary against the PARENT process's PATH
		// (not cmd.Env), so emptying it here is what makes the lookup fail.
		t.Setenv("PATH", "")

		got := s.gitFailure(t.TempDir(), errProbeTrigger)
		if got == nil {
			t.Fatal("want an error when git cannot be executed, got nil")
		}

		var appErr *models.AppError
		if errors.As(got, &appErr) && appErr.Code == models.ErrGitNotRepo {
			t.Fatalf("a missing git binary must NOT be reported as GIT_NOT_REPO: %v", got)
		}

		var execErr *exec.Error
		if !errors.As(got, &execErr) {
			t.Fatalf("the exec lookup failure must survive the wrap, got %q", got.Error())
		}
	})

	t.Run("nil input error stays nil", func(t *testing.T) {
		if got := s.gitFailure(t.TempDir(), nil); got != nil {
			t.Fatalf("want nil for a nil input error, got %v", got)
		}
	})
}

// TestGitExitCode_DiscriminatesRanFromNeverRan is the positive control for the
// discriminator gitFailure now depends on. It proves gitExitCode actually
// separates the two shapes through gitCommandWithCreds' %w wrapping, so a
// gitFailure branch that reads < 0 is testing something real.
//
// Without this, a gitExitCode that returned -1 for EVERYTHING would still make
// the split test above pass its probe-failure rows — and would silently turn
// every genuine non-repository into a 500.
func TestGitExitCode_DiscriminatesRanFromNeverRan(t *testing.T) {
	s := &GitService{}

	t.Run("git ran and exited non-zero", func(t *testing.T) {
		_, err := s.gitCommandWithCreds(t.TempDir(), "", "", "rev-parse", "--git-dir")
		if err == nil {
			t.Fatal("expected rev-parse to fail outside a repository")
		}
		if code := gitExitCode(err); code < 0 {
			t.Fatalf("want a real exit status for a git process that ran, got %d (%v)", code, err)
		}
	})

	t.Run("git never ran", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "not-mounted")

		_, err := s.gitCommandWithCreds(missing, "", "", "rev-parse", "--git-dir")
		if err == nil {
			t.Fatal("expected rev-parse to fail in a directory that does not exist")
		}
		if code := gitExitCode(err); code >= 0 {
			t.Fatalf("want -1 for a process that never ran, got %d (%v)", code, err)
		}
	})
}

// TestGetStatus_UnbornHeadHasItsOwnCode pins the third arm of agent-os-n2df:
// "this repository has no commits yet" must answer with a code of its own
// rather than the generic models.ErrNotFound.
//
// It is a routine negative answer — a repository that has been `git init`ed but
// not committed to is a normal state, and GET /api/v1/git is asked of it on
// every page visit, exactly like the non-repo case. But it CANNOT be marked
// routine while it shares ErrNotFound with "Stack not found", which is a
// genuine client error minted at 20 other sites: handlers/respond.go keys
// routineErrorCodes on the CODE, so listing ErrNotFound would silence both.
//
// The alternative — calling middleware.MarkRoutineOutcome at the site, the way
// agent-os-hjmf will for handlers/env.go — is structurally impossible here.
// This is the service layer and it has no gin.Context; internal/services
// imports gin nowhere. A dedicated code is the only available route, which is
// why this changes internal/models/errors.go.
//
// The message is deliberately unchanged, so git_parity_yo9e_test.go's
// "06 unborn HEAD" arm (which asserts on err.Error(), the Message) stays green.
// Only the code moves.
func TestGetStatus_UnbornHeadHasItsOwnCode(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")

	_, err := svc.GetStatus(dir)
	if err == nil {
		t.Fatal("want an error for a repository with no commits, got nil")
	}

	var appErr *models.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("want *models.AppError, got %T: %v", err, err)
	}
	if appErr.Code == models.ErrNotFound {
		t.Fatalf("the unborn-head 404 must not share models.ErrNotFound with "+
			"the genuine \"Stack not found\" client error: got %q", appErr.Code)
	}
	if appErr.Code != models.ErrGitNoCommits {
		t.Fatalf("want code %q, got %q", models.ErrGitNoCommits, appErr.Code)
	}
	if appErr.Status != 404 {
		t.Fatalf("want status 404 (unchanged), got %d", appErr.Status)
	}
	// The user-visible message is the part agent-os-xmtf fixed; it must not move.
	if appErr.Message != "Repository has no commits yet" {
		t.Fatalf("message must be unchanged, got %q", appErr.Message)
	}
}
