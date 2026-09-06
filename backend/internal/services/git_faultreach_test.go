package services

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-hsj7. Nine error branches in git.go that no test drove a fault into.
//
// WHAT THIS FILE IS, and what it deliberately is not. It adds no production
// change: every site here was already converted and already discriminating.
// What was missing is the other half of the sweep — "every converted site has a
// fault arm that reaches it" is a DIFFERENT question from "every site is
// converted", and only the first was ever answered for this file. Mutating any
// of these nine returns to swallow its error left the suite green.
//
// HOW THE SITE SET WAS DERIVED, and why it is not the twelve the bead names.
// The former shell driver scripts/check-getter-fault-reach.sh reported MISS=12
// for services/git.go, because its test selection was the filename pattern
// *_dbfault_test.go while git.go's errors are exec errors, not DB errors — so
// it could not see the ordinary tests in this package that already drive faults
// into five of the twelve. That selector IS agent-os-1hig, which deleted the
// script; this file's own name is a sixth victim of that glob. The surviving
// instrument is the analyser's Go mode, measured over a FULL-package coverage
// profile, on 19082bb:
//
//	go test ./internal/services -count=1 -coverprofile=all.cov
//	go run ../scripts/getter-errors/main.go reach all.cov $PWD/internal/services/git.go
//	  -> CONVERTED=12 REACHED=5 MISS=7
//
// THE TRAP THAT SET THE COUNT AT NINE RATHER THAN SEVEN. That REACHED verdict
// is per SITE, while the coverage evidence under it is per BLOCK, and two of
// the twelve `if err != nil` bodies have several exits. git.go:71 has three
// (the gitFailure 404, the unborn-HEAD 404, and a fall-through) and git.go:721
// has two. In both, a sibling branch runs and marks the whole site REACHED
// while the branch below it has never executed once. From the same profile
// whose rows read REACHED, verbatim:
//
//	git.go:101.3,101.60 1 0     <- "failed to resolve HEAD", never executed
//	git.go:726.3,726.55 1 0     <- "failed to get log", never executed
//
// This is agent-os-3h9x's borrowed guard one level up: there an aggregation
// window reached past a site and borrowed the NEXT call's guard; here a site
// borrows the coverage of a SIBLING branch inside its own body. Taking the
// site-level REACHED as the verdict would have closed this bead with two live
// uncovered error returns recorded as covered. So the seven untouched sites and
// those two branches are nine, and each test below asserts the SPECIFIC message
// or outcome its branch produces — never merely that an error came back, which
// would pass against a fault raised anywhere else on the path.
//
// FAULT MECHANISMS, all measured rather than assumed (git 2.47.3, this host):
//
//	                        status --porcelain  rev-parse HEAD  log -- .  --git-dir
//	not a repository        128                 128             128       128
//	git init, no commits    0                   128             128       0
//
// Those two fixtures are real states an operator reaches, and they drive five
// of the nine with no instrumentation at all. The remaining four need one
// specific git subcommand to fail while its neighbours succeed, which no
// natural fixture produces, so they use the PATH-shadowing wrapper this package
// already relies on (git_credentials_test.go:628): a script named `git`
// earlier on PATH that fails one argv shape and execs the real binary for
// everything else. chmod is not used anywhere here — it is a no-op when tests
// run as uid 0, which is true in a container and may be true on a CI runner
// even where it is false on a developer host, so a permission-bit fault can
// stop driving a fault without failing.

// faultingGitOnPath puts a wrapper script named `git` at the front of PATH for
// the rest of the test. The wrapper fails, with an *exec.ExitError the site's
// %w wrap preserves, exactly those invocations whose joined arguments match
// shellPattern; every other invocation is exec'd through to the real git, so
// the function under test proceeds normally up to the one call being faulted.
//
// The argv the pattern is matched against is gitCmdWithCreds' own, which is
// `-c safe.directory=<dir>` followed by the caller's arguments (and two more
// `-c credential.helper=` pairs when a token is configured — none of these
// tests configure one). Patterns below anchor on the trailing subcommand for
// that reason.
//
// The real git binary is resolved BEFORE PATH is shadowed. Resolving it after
// would find the wrapper, and the wrapper would exec itself forever.
func faultingGitOnPath(t *testing.T, shellPattern string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  " + shellPattern + ") echo 'capstan-test: injected git fault' >&2; exit 3;;\n" +
		"esac\n" +
		"exec \"" + realGit + "\" \"$@\"\n"
	path := filepath.Join(dir, "git")
	//nolint:gosec // test-owned wrapper script under t.TempDir(), not user input
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// emptyGitRepo is `git init` with no commit: a directory that IS a repository,
// so gitFailure's `rev-parse --git-dir` probe answers yes, while every command
// that needs a commit fails. It drives the three sites whose branch is reached
// only when the repository check passes and the command still failed.
func emptyGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	return dir
}

// requireExecFault fails the test when err does not carry an *exec.ExitError.
// Without it, a fixture that broke earlier — a missing binary, an unwritable
// temp dir — would satisfy an assertion on the message text alone and the test
// would pass while reaching nothing.
//
// It runs AFTER any assertion about which branch was taken, never before. Those
// assertions are statements about control flow and hold whatever the cause
// chain looks like; putting this first would answer a site that stopped
// wrapping its cause with a message about the fixture, which is the wrong
// diagnosis for the reader.
func requireExecFault(t *testing.T, err error) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error carries no *exec.ExitError: %v — either the fixture faulted before reaching the site under test, or the site stopped wrapping the cause", err)
	}
}

// TestGetStatusCLI_UnresolvableBranchIsNotClaimedAsNoCommits pins git.go:101,
// the third exit of the site at git.go:71 and the one its own comment calls
// "unusual enough that naming it would be another guess".
//
// The other two exits are pinned already (TestGetStatus_NonGitRepoReturns404
// takes the gitFailure 404, TestGetStatus_EmptyRepoIsNotClaimedNotARepo takes
// the unborn-HEAD 404) and between them they mark the whole site REACHED. This
// is the branch underneath: a directory that IS a repository, whose HEAD DOES
// resolve to a commit, whose branch name still could not be read.
//
// The assertion has to discriminate all three, because a mutant that routed
// this case into either 404 would still return an error. So it requires the
// specific message AND requires the result not to be an AppError at all —
// both 404s are.
func TestGetStatusCLI_UnresolvableBranchIsNotClaimedAsNoCommits(t *testing.T) {
	dir := repoWithCommit(t, t.TempDir())
	faultingGitOnPath(t, "*\"rev-parse --abbrev-ref HEAD\"")

	svc := NewGitService(&config.Config{}, nil)
	_, err := svc.GetStatus(dir)
	if err == nil {
		t.Fatal("GetStatus succeeded while `rev-parse --abbrev-ref HEAD` was faulted")
	}
	var appErr *models.AppError
	if errors.As(err, &appErr) {
		t.Fatalf("got AppError %d %s (%q); this repository has commits and IS a repository, so neither 404 applies — the fall-through at git.go:101 is the honest answer",
			appErr.Status, appErr.Code, appErr.Message)
	}
	requireExecFault(t, err)

	if !strings.Contains(err.Error(), "failed to resolve HEAD") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to resolve HEAD")
	}
}

// TestGetStatusCLI_UnreadableHeadCommitIsReported pins git.go:104.
//
// The branch name resolves and the commit hash does not, which is the one
// ordering that reaches this site: any fault that also took out the branch read
// would return at git.go:71 instead and never arrive. The wrapper pattern
// anchors on the exact argv `rev-parse HEAD`, so `rev-parse --abbrev-ref HEAD`
// and `rev-parse --verify HEAD` are untouched.
func TestGetStatusCLI_UnreadableHeadCommitIsReported(t *testing.T) {
	dir := repoWithCommit(t, t.TempDir())
	faultingGitOnPath(t, "*\" rev-parse HEAD\"")

	svc := NewGitService(&config.Config{}, nil)
	_, err := svc.GetStatus(dir)
	if err == nil {
		t.Fatal("GetStatus succeeded while `rev-parse HEAD` was faulted")
	}
	requireExecFault(t, err)

	if strings.Contains(err.Error(), "failed to resolve HEAD") {
		t.Fatalf("error = %q — that is git.go:71's branch, so the branch read failed too and this test never reached git.go:104", err)
	}
	if !strings.Contains(err.Error(), "failed to get HEAD") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to get HEAD")
	}
}

// TestPullCLI_UnreadableStatusIsReported pins git.go:248.
//
// pullCLI reads the worktree status first, and that read has two outcomes the
// caller must not see merged: output it could read and found dirty (a 400 the
// operator fixes by committing), and a read that failed (not a statement about
// the worktree at all). A non-repository makes the second happen for real.
func TestPullCLI_UnreadableStatusIsReported(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	_, err := svc.Pull(t.TempDir())
	if err == nil {
		t.Fatal("Pull succeeded on a directory that is not a git repository")
	}
	var appErr *models.AppError
	if errors.As(err, &appErr) && appErr.Code == models.ErrGitDirty {
		t.Fatalf("an unreadable status was reported as %q — the worktree was never read, so nothing is known about whether it is dirty", appErr.Code)
	}
	requireExecFault(t, err)

	if !strings.Contains(err.Error(), "failed to check status") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to check status")
	}
}

// TestPullCLI_UnreadableHeadIsReported pins git.go:256.
//
// Reaching it needs the status read to SUCCEED and the HEAD read to fail, which
// the non-repository fixture above cannot produce — there, status fails first
// and pullCLI returns one site earlier. A repository with no commits is the
// natural state that separates them: `status --porcelain` exits 0 with empty
// output, so the tree reads clean, and `rev-parse HEAD` exits 128.
func TestPullCLI_UnreadableHeadIsReported(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	_, err := svc.Pull(emptyGitRepo(t))
	if err == nil {
		t.Fatal("Pull succeeded on a repository with no commits")
	}
	requireExecFault(t, err)

	if strings.Contains(err.Error(), "failed to check status") {
		t.Fatalf("error = %q — the status read failed, so this fixture returned at git.go:248 and never reached git.go:256", err)
	}
	if !strings.Contains(err.Error(), "failed to get HEAD") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to get HEAD")
	}
}

// TestPullVerified_PullFaultIsFailedNotSuccess pins git.go:424.
//
// PullVerified's whole contract is that it never reports success when the work
// did not happen, and this is the first place that contract can be broken: a
// pull that failed outright. The assertion is on the outcome, not on an error
// value, because the outcome is what the caller branches on and what reaches
// the API as a status code.
func TestPullVerified_PullFaultIsFailedNotSuccess(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	ar, pullResult := svc.PullVerified(t.TempDir(), false, nil)
	if ar.Outcome != truth.OutcomeFailed {
		t.Errorf("outcome = %q, want %q — the pull never ran", ar.Outcome, truth.OutcomeFailed)
	}
	if !strings.Contains(ar.Reason, "git pull failed") {
		t.Errorf("reason = %q, want it to contain %q", ar.Reason, "git pull failed")
	}
	if ar.Err == nil {
		t.Error("ActionResult.Err is nil; the cause is the only thing that tells the operator what to fix")
	}
	if pullResult != nil {
		t.Errorf("pullResult = %+v, want nil — there is no result to report from a pull that failed", pullResult)
	}
}

// TestPullVerified_StackListFaultIsPartialNotSuccess pins git.go:447.
//
// This is the site with the most preconditions, and every one of them is load
// bearing: the pull must SUCCEED, HEAD must advance, the pull must report at
// least one changed file, and redeploy must be requested with a non-nil docker
// — otherwise PullVerified returns success at git.go:437 and the stack lookup
// never runs. So the fixture is a real fast-forward: a bare origin, a seed
// clone that pushes a second commit to it, and a working clone that pulls it.
//
// The fault is a closed database, the shape this package already uses
// (docker_update_dbfault_test.go:85). It matters that the failure is a driver
// error rather than an empty result: an empty stack list is a legitimate answer
// meaning "no stacks here, nothing to redeploy", and reporting success for it
// is correct. Only a read that FAILED makes the redeploy untried, and untried
// is not the same as done.
//
// docker is a zero-value DockerService. It is never dereferenced: the stack
// lookup fails before any RestartVerified call, which is itself part of what
// this pins — a non-nil docker must not be enough to claim a redeploy happened.
func TestPullVerified_StackListFaultIsPartialNotSuccess(t *testing.T) {
	origin := t.TempDir()
	mustGit(t, origin, "init", "-q", "--bare", "-b", "main")

	seed := t.TempDir()
	mustGit(t, seed, "init", "-q", "-b", "main")
	writeStackFile(t, seed, "docker-compose.yml", "services: {}\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	mustGit(t, seed, "remote", "add", "origin", origin)
	mustGit(t, seed, "push", "-q", "origin", "main")

	work := t.TempDir()
	mustGit(t, work, "clone", "-q", origin, ".")

	writeStackFile(t, seed, "docker-compose.yml", "services: {app: {image: busybox}}\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "second")
	mustGit(t, seed, "push", "-q", "origin", "main")

	svc := NewGitService(&config.Config{}, closedTestDB(t))
	ar, pullResult := svc.PullVerified(work, true, &DockerService{})

	if ar.Outcome == truth.OutcomeNoChange {
		t.Fatalf("outcome = %q: the pull did not advance HEAD, so the stack lookup at git.go:447 was never reached and this fixture proves nothing", ar.Outcome)
	}
	if ar.Outcome != truth.OutcomePartial {
		t.Fatalf("outcome = %q (reason %q), want %q — the pull landed but the redeploy was never attempted, and reporting that as success claims work that did not happen",
			ar.Outcome, ar.Reason, truth.OutcomePartial)
	}
	if !strings.Contains(ar.Reason, "could not list stacks") {
		t.Errorf("reason = %q, want it to contain %q", ar.Reason, "could not list stacks")
	}
	if listErr, ok := ar.Details["listError"]; !ok || listErr == "" {
		t.Errorf("details[listError] = %v, want the cause of the failed read", ar.Details["listError"])
	}
	if pullResult == nil {
		t.Error("pullResult is nil; the pull itself succeeded, so its commits are still the caller's answer")
	}
}

// TestGetLogCLI_UnreadableLogIsReported pins git.go:629.
//
// The commit count read succeeds and the log read fails. The two sites report
// different things — one that the repository could not be counted, one that its
// log could not be read — and only the second has no fault arm. The wrapper
// anchors on `log --skip=`, which getLogCLI's invocation carries and no other
// call in this path does.
func TestGetLogCLI_UnreadableLogIsReported(t *testing.T) {
	dir := repoWithCommit(t, t.TempDir())
	faultingGitOnPath(t, "*\" log --skip=\"*")

	svc := NewGitService(&config.Config{}, nil)
	_, err := svc.GetLog(dir, 50, 0)
	if err == nil {
		t.Fatal("GetLog succeeded while `log --skip=...` was faulted")
	}
	var appErr *models.AppError
	if errors.As(err, &appErr) && appErr.Code == models.ErrGitNotRepo {
		t.Fatalf("a faulted log read inside a real repository was reported as %q", appErr.Code)
	}
	requireExecFault(t, err)

	if strings.Contains(err.Error(), "failed to count commits") {
		t.Fatalf("error = %q — the rev-list failed too, so this returned at git.go:617 and never reached git.go:629", err)
	}
	if !strings.Contains(err.Error(), "failed to get log") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to get log")
	}
}

// TestGetDiffCLI_UnreadableDiffIsReported pins git.go:694.
//
// The commit metadata read succeeds and the diff read fails. Its neighbour at
// git.go:673 is pinned by TestGitDiff_BadHashInsideRepoIsNotNotARepo, which
// makes the FIRST command fail — so it can never arrive here. The wrapper
// anchors on `show --format=`, unique to this call.
func TestGetDiffCLI_UnreadableDiffIsReported(t *testing.T) {
	dir := repoWithCommit(t, t.TempDir())
	head := gitOutput(t, dir, "rev-parse", "HEAD")
	faultingGitOnPath(t, "*\" show --format=\"*")

	svc := NewGitService(&config.Config{}, nil)
	_, err := svc.GetDiff(dir, head)
	if err == nil {
		t.Fatal("GetDiff succeeded while `show --format=` was faulted")
	}
	requireExecFault(t, err)

	if strings.Contains(err.Error(), "failed to get commit") {
		t.Fatalf("error = %q — the commit read failed too, so this returned at git.go:673 and never reached git.go:694", err)
	}
	if !strings.Contains(err.Error(), "failed to get diff") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to get diff")
	}
}

// TestGetLogForFile_LogFaultInsideARepoIsNotNotARepo pins git.go:726, the
// second exit of the site at git.go:721.
//
// The first exit is pinned by TestGitEntryPoints_NotARepoIsA404 and marks the
// whole site REACHED, which is what hid this one. The distinction the branch
// exists to draw is between a directory that is not a repository (404, point
// the stack elsewhere) and a repository whose log could not be read (something
// else entirely). A repository with no commits is the second: `rev-parse
// --git-dir` exits 0 so gitFailure declines, and `git log` exits 128.
//
// Its sibling entry point is already covered for this exact state — GetLog is
// pinned by TestGitEntryPoints_RepoWithNoCommitsIsNotClaimedNotARepo — and
// GetLogForFile was not, which is the whole reason a per-branch reading of the
// coverage was needed to find it.
func TestGetLogForFile_LogFaultInsideARepoIsNotNotARepo(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	_, err := svc.GetLogForFile(emptyGitRepo(t), ".", 50)
	if err == nil {
		t.Fatal("GetLogForFile succeeded on a repository with no commits")
	}
	if _, code := statusFor(err); code == models.ErrGitNotRepo {
		t.Fatalf("a repository with no commits was reported as %q; it IS a repository, and saying otherwise sends the operator to fix the wrong thing", code)
	}
	requireExecFault(t, err)

	if !strings.Contains(err.Error(), "failed to get log") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to get log")
	}
}

// writeStackFile writes one file into a fixture repository.
func writeStackFile(t *testing.T, dir, name, content string) {
	t.Helper()
	//nolint:gosec // fixture file under the test's own t.TempDir(), not user input
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// gitOutput runs one git command in dir and returns its trimmed stdout.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // test helper, explicit argv, not a shell string
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// closedTestDB is a migrated database whose connection is closed, so every read
// fails with a driver error rather than returning an empty result. The
// distinction is the point: an empty stack list is a legitimate answer, and
// only a failed read makes a redeploy untried.
func closedTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrationsAndEncryptor(t.TempDir(), NewTokenEncryptorOrDefault("hsj7-storage-key-0123456789abcd", ""))
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db to induce failure: %v", err)
	}
	return db
}
