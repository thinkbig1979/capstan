package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// This file pins agent-os-vwi7: gitCommandWithCreds read its child with
// cmd.CombinedOutput(), merging stderr into stdout, so a git command that EXITS
// 0 while writing a diagnostic handed that diagnostic to every parsing caller as
// DATA. No error value exists anywhere on these paths, so no error-keyed
// instrument — not agent-os-ufj7's fix, not the geterrors analyzer, not errcheck
// — could ever fire on them.
//
// Every fault below is a CONTENT fault, never a permission fault. The server
// runs as root in the production container and root bypasses a permission bit,
// so a chmod-based fixture would read "cannot reproduce" in the environment the
// defect ships in. chmod 000 on a tracked subdirectory IS a live vector for this
// class (re-probed under the stderr lens; agent-os-ufj7 dismissed it as "warning
// only" when the warning IS the payload), but for exactly that reason it cannot
// serve as a regression fixture here.
//
// The stream attribution behind each fixture was measured directly, git 2.47.3,
// with the same LC_ALL=C / LANGUAGE= child environment gitCmdWithCreds pins:
//
//	clean repo, no fault                     exit=0  stdout 0 bytes   stderr 0 bytes
//	clean repo, core.fsmonitor bogus         exit=0  stdout 0 bytes   stderr 132 bytes
//	clean repo, hook printing on fd1 AND fd2 exit=0  stdout 0 bytes   stderr both lines
//	rev-list, unambiguous ref                exit=0  stdout "0\t1"    stderr 0 bytes
//	rev-list, ambiguous refname              exit=0  stdout "0\t1"    stderr 45 bytes
//
// The third row is the one worth reading twice: git's hook machinery redirects a
// hook's OWN stdout onto git's stderr, so "any hook that prints" — the most
// reachable vector of all, since most repositories with a linter or formatter
// hook write something — is entirely a stderr vector and is closed by the split.

// vwi7Repo builds a repository with one committed TRACKED file, modified
// afterwards only when dirty is true.
//
// Tracked-and-modified, never merely untracked: DirtyCount == 1 cannot tell
// those two fixtures apart, so an untracked-file "simplification" would pass
// every assertion here while moving the control onto a different git behaviour.
//
// -b main explicitly, because `git init` alone takes the branch name from the
// ambient init.defaultBranch; on a host configured for "master" the tracking ref
// planted below would name a branch that does not exist and the ahead/behind
// arms would stop reaching the rev-list block without failing.
func vwi7Repo(t *testing.T, dirty bool) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	if dirty {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\nmodified\n"), 0o600); err != nil {
			t.Fatalf("modify f.txt: %v", err)
		}
	}
	return dir
}

// vwi7FsmonitorFault points core.fsmonitor at a path that cannot be executed.
// git reports it twice on stderr and still exits 0, because a broken fsmonitor
// hook degrades to a full scan rather than failing the command.
func vwi7FsmonitorFault(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir, "config", "core.fsmonitor", "/nonexistent/hook")
}

// vwi7PrintingHook installs a post-index-change hook that writes to its own
// stdout. `git status` runs it and exits 0.
func vwi7PrintingHook(t *testing.T, dir string) {
	t.Helper()
	hook := filepath.Join(dir, ".git", "hooks", "post-index-change")
	//nolint:gosec // a hook must be executable to run at all, and this one is written into this test's own t.TempDir() under a fixed name — not user input, and gone when the test ends
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'noise-from-a-printing-hook'\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write post-index-change hook: %v", err)
	}
}

// vwi7StatusPrecondition measures the two streams of `git status --porcelain`
// SEPARATELY and asserts what each one carries.
//
// It runs its own child rather than calling gitCommandWithCreds, and that is the
// whole point: gitCommandWithCreds is the function under repair, so a
// precondition routed through it could not distinguish "the fault is present"
// from "the helper merges the streams", and would flip from red to green purely
// because the fix landed. A precondition has to hold identically before and
// after, or it is an assertion about the fix wearing a precondition's name.
//
// The child mirrors the two properties of gitCmdWithCreds that could change what
// git writes: the `-c safe.directory` argument and the LC_ALL=C / cleared
// LANGUAGE pin. It deliberately does NOT reuse gitCmdWithCreds itself, for the
// same independence reason.
//
// Both halves are asserted, which is what makes this two-sided. wantStdout pins
// the data; wantStderr pins that the FAULT IS ACTUALLY PRESENT. Without the
// second, a fixture whose fault silently failed to apply — a git that ignored
// the config, a hook that was never made executable — would let every "reads
// clean" assertion below pass by measuring an unfaulted repository, which is the
// one result that proves nothing.
func vwi7StatusPrecondition(t *testing.T, dir, wantStdout string, wantStderr bool) {
	t.Helper()
	//nolint:gosec // test helper, explicit argv, dir is this test's own t.TempDir()
	cmd := exec.Command("git", "-c", "safe.directory="+dir, "status", "--porcelain")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANGUAGE=")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("precondition: `status --porcelain` must EXIT 0 — this class is the ZERO-EXIT arm, and a non-zero exit means the fixture is reproducing agent-os-ufj7's fault instead: %v (stderr %q)", err, stderr.String())
	}
	// TrimRight, never TrimSpace: porcelain's first two columns are the index and
	// worktree status, so the LEADING space in " M f.txt" is data — it is what
	// says the change is unstaged. Trimming both ends would silently accept a
	// staged " M" vs "M " mix-up as the same fixture.
	if got := strings.TrimRight(stdout.String(), "\n"); got != wantStdout {
		t.Fatalf("precondition: `status --porcelain` STDOUT = %q, want %q — the fixture is not in the worktree state this arm assumes", got, wantStdout)
	}
	if got := strings.TrimSpace(stderr.String()); (got != "") != wantStderr {
		t.Fatalf("precondition: `status --porcelain` STDERR = %q, want non-empty = %v — the fault did not apply, so this arm would measure an unfaulted repository and prove nothing", got, wantStderr)
	}
}

// TestGetStatus_ZeroExitStderrIsNotCountedAsChanges pins fault 1 of agent-os-vwi7.
//
// A CLEAN worktree whose `git status --porcelain` writes a zero-exit diagnostic
// to stderr was reported as having uncommitted changes, because CombinedOutput
// handed that diagnostic to the parser as data. On the fsmonitor fault the two
// `fatal: cannot exec` lines counted as dirtyCount=2 for a repository with no
// changes at all.
//
// Four arms on one instrument, and they must come out DIFFERENTLY. Arms 1 and 2
// alone are satisfied by an implementation that reports every repository clean —
// which is worse than the bug, since it would hide real changes — so arms 3 and
// 4 are the load-bearing half, not decoration.
func TestGetStatus_ZeroExitStderrIsNotCountedAsChanges(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// ARM 1 — unfaulted clean control. Establishes that "clean" is reachable.
	clean := vwi7Repo(t, false)
	vwi7StatusPrecondition(t, clean, "", false)
	st, err := svc.GetStatus(clean)
	if err != nil {
		t.Fatalf("control: GetStatus on an undamaged clean repo returned err=%v; the fixture is broken, not the code", err)
	}
	if st.Dirty || st.DirtyCount != 0 {
		t.Fatalf("control: undamaged clean repo reported dirty=%v count=%d, want false/0", st.Dirty, st.DirtyCount)
	}

	// ARM 2 — THE DEFECT. Same clean repository, fsmonitor pointed at a hook
	// that cannot be executed. stdout is still empty; only stderr carries the
	// two fatal lines.
	faulted := vwi7Repo(t, false)
	vwi7FsmonitorFault(t, faulted)
	vwi7StatusPrecondition(t, faulted, "", true)
	st, err = svc.GetStatus(faulted)
	if err != nil {
		t.Fatalf("faulted clean repo: GetStatus returned err=%v, want a clean status — the diagnostic belongs on stderr, not in the error", err)
	}
	if st.Dirty || st.DirtyCount != 0 {
		t.Errorf("a CLEAN worktree reported dirty=%v dirtyCount=%d, want false/0 — git's zero-exit stderr diagnostic was counted as %d uncommitted changes that do not exist",
			st.Dirty, st.DirtyCount, st.DirtyCount)
	}

	// ARM 3 — unfaulted dirty control. Proves the instrument still reports a
	// real modification, so arm 2's "clean" is a real clean.
	dirty := vwi7Repo(t, true)
	vwi7StatusPrecondition(t, dirty, " M f.txt", false)
	st, err = svc.GetStatus(dirty)
	if err != nil {
		t.Fatalf("control: GetStatus on an undamaged dirty repo returned err=%v", err)
	}
	if !st.Dirty || st.DirtyCount != 1 {
		t.Fatalf("control: undamaged dirty repo reported dirty=%v count=%d, want true/1", st.Dirty, st.DirtyCount)
	}

	// ARM 4 — the DISCRIMINATING control, and the one that pins the COUNT
	// rather than the boolean. Faulted AND genuinely modified: dirty is true
	// either way, so the boolean cannot tell the fix from the bug here. The
	// count can. Before the split this read 3 — one real change plus two
	// fabricated lines — so a fix that only stopped `dirty` flipping, without
	// isolating stdout, still fails this arm.
	both := vwi7Repo(t, true)
	vwi7FsmonitorFault(t, both)
	vwi7StatusPrecondition(t, both, " M f.txt", true)
	st, err = svc.GetStatus(both)
	if err != nil {
		t.Fatalf("faulted dirty repo: GetStatus returned err=%v", err)
	}
	if !st.Dirty {
		t.Errorf("faulted repo with one REAL modification reported dirty=false — the fix must not blind the probe to genuine changes")
	}
	if st.DirtyCount != 1 {
		t.Errorf("dirtyCount=%d for ONE modified file, want 1 — git's stderr diagnostic is being counted as extra changed files", st.DirtyCount)
	}
}

// TestGetStatus_PrintingHookIsNotCountedAsChanges pins the most reachable vector
// of agent-os-vwi7: a repository with a hook that prints. This needs no
// misconfiguration whatsoever — a linter or formatter hook that writes a line is
// ordinary — which is why it matters more than the fsmonitor fault it shares a
// mechanism with.
func TestGetStatus_PrintingHookIsNotCountedAsChanges(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	dir := vwi7Repo(t, false)
	vwi7PrintingHook(t, dir)
	vwi7StatusPrecondition(t, dir, "", true)

	st, err := svc.GetStatus(dir)
	if err != nil {
		t.Fatalf("GetStatus on a clean repo with a printing hook returned err=%v", err)
	}
	if st.Dirty || st.DirtyCount != 0 {
		t.Errorf("clean repo with a printing post-index-change hook reported dirty=%v dirtyCount=%d, want false/0 — the hook's own output was parsed as porcelain lines",
			st.Dirty, st.DirtyCount)
	}
}

// TestPull_ZeroExitStderrDoesNotRefuseACleanRepo pins the WORST consequence of
// agent-os-vwi7, and the reason it outranks "a chip reads wrong".
//
// pullCLI guards on `strings.TrimSpace(dirtyOutput) != ""` against the same
// conflated stream, so a CLEAN repository under this fault was REFUSED A PULL
// with a 400 telling the operator to commit changes that do not exist. There is
// no recovery path in the UI: nothing can be committed, and nothing anywhere
// names the underlying config. A dead end, not a cosmetic wrong value.
//
// The pull itself cannot succeed in either arm — these fixtures have no remote —
// so the assertion is specifically about WHICH error comes back. That is the
// whole question: 400 GIT_DIRTY is a false statement about the worktree, while a
// failure to reach a remote that does not exist is true.
func TestPull_ZeroExitStderrDoesNotRefuseACleanRepo(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// ARM 1 — THE DEFECT. Clean worktree, fsmonitor fault.
	faulted := vwi7Repo(t, false)
	vwi7FsmonitorFault(t, faulted)
	vwi7StatusPrecondition(t, faulted, "", true)

	_, err := svc.Pull(faulted)
	if err == nil {
		t.Fatalf("precondition: this fixture has no remote, so the pull cannot succeed; err=nil means the test is measuring something else")
	}
	if status, code := statusFor(err); code == models.ErrGitDirty {
		t.Errorf("a CLEAN worktree was refused a pull with %d %s (%v) — the 400 names uncommitted changes that do not exist, and the operator has nothing to commit to clear it",
			status, code, err)
	}

	// ARM 2 — the CONTROL, and it must fire. A genuinely dirty worktree still
	// gets the 400. Without this, arm 1 is satisfied by deleting the dirty
	// guard outright, which would let a pull clobber real local edits.
	dirty := vwi7Repo(t, true)
	vwi7StatusPrecondition(t, dirty, " M f.txt", false)

	_, err = svc.Pull(dirty)
	if err == nil {
		t.Fatalf("control: a dirty worktree must still be refused, got err=nil")
	}
	status, code := statusFor(err)
	if code != models.ErrGitDirty || status != 400 {
		t.Errorf("control: a genuinely dirty worktree got %d %s (%v), want 400 %s — the dirty guard must still protect real local changes",
			status, code, err, models.ErrGitDirty)
	}
}

// TestGetStatus_AmbiguousRefnameStillCountsAheadBehind pins fault 2 of
// agent-os-vwi7.
//
// A local branch literally named origin/main, beside the remote-tracking ref of
// the same name, makes the symmetric-difference expression ambiguous. git warns
// on stderr and EXITS 0. Merged into stdout, `strings.Fields` then saw seven
// fields instead of two, the `len(parts) == 2` guard was false, and ahead/behind
// kept their pre-initialised zeros on a repository that is ahead by one —
// which GitStatus.tsx gates on `{ahead > 0 && ...}` and draws as "up to date".
//
// MEASURED: with the streams split, the 45-byte warning is entirely on stderr
// and stdout is a clean "0\t1". So the fix does not merely stop the block being
// SKIPPED, it makes the block produce the RIGHT NUMBERS. This arm therefore
// asserts ahead=1, not merely "an error was returned" — a fix that returned an
// error here would be wrong, because git answered the question correctly.
//
// Both refs are planted at the same commit, so the counts are identical whichever
// one git's precedence picks. The test cannot be broken by a change in that
// precedence, which is not a property worth depending on.
func TestGetStatus_AmbiguousRefnameStillCountsAheadBehind(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// ARM 1 — the CONTROL, on the unambiguous ref. It must PASS against the
	// pre-fix code as well: that is what makes arm 2's red the defect rather
	// than a broken fixture.
	ok := vwi7AheadByOne(t, false)
	st, err := svc.GetStatus(ok)
	if err != nil {
		t.Fatalf("control: GetStatus returned err=%v; the fixture is broken, not the code", err)
	}
	if st.TrackingBranch != "origin/main" {
		t.Fatalf("control: trackingBranch=%q, want %q — the fixture never entered the rev-list block, so arm 2 would prove nothing", st.TrackingBranch, "origin/main")
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Fatalf("control: ahead=%d behind=%d, want 1/0 on an unambiguous ref one commit behind HEAD", st.Ahead, st.Behind)
	}

	// ARM 2 — THE DEFECT. Identical repository plus a local branch named
	// origin/main, which is what makes the revision expression ambiguous.
	amb := vwi7AheadByOne(t, true)

	// Precondition: the ambiguity must not have broken the ref resolution that
	// gets us into the block at all, and rev-list must still EXIT 0. A non-zero
	// exit here would mean this arm is reproducing agent-os-ufj7's fault rather
	// than this one.
	if _, err := svc.gitCommandWithCreds(amb, "", "", "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main"); err != nil {
		t.Fatalf("precondition: the tracking ref must still resolve or trackingBranch stays empty and the rev-list never runs: %v", err)
	}
	if _, err := svc.gitCommandWithCreds(amb, "", "", "rev-list", "--left-right", "--count", "origin/main...HEAD"); err != nil {
		t.Fatalf("precondition: rev-list must still EXIT 0 under the ambiguity — this class is the zero-exit arm: %v", err)
	}

	st, err = svc.GetStatus(amb)
	if err != nil {
		t.Fatalf("ambiguous refname: GetStatus returned err=%v — git answered the question correctly on stdout, so this must not be an error", err)
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Errorf("ahead=%d behind=%d on a repository that is ahead by one, want 1/0 — git's zero-exit stderr warning was merged into the counts, so the field count missed 2 and both values kept their zeros, which renders as \"up to date\"",
			st.Ahead, st.Behind)
	}
}

// vwi7AheadByOne builds a repository one commit ahead of refs/remotes/origin/main.
// With ambiguous set it also creates a LOCAL branch named origin/main at the same
// commit, which is what makes `origin/main...HEAD` ambiguous.
func vwi7AheadByOne(t *testing.T, ambiguous bool) string {
	t.Helper()
	dir := vwi7Repo(t, false)
	first := strings.TrimSpace(mustGitOut(t, dir, "rev-parse", "HEAD"))
	writeRemoteRef(t, dir, first)
	if ambiguous {
		mustGit(t, dir, "branch", "origin/main", first)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\nsecond\n"), 0o600); err != nil {
		t.Fatalf("write second revision: %v", err)
	}
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qam", "second")
	return dir
}

// TestGitCommandWithCreds_RedactsTokenOnStdoutOnly is the stdout half of the
// redaction proof.
//
// The existing TestGitCommand_RedactsTokenFromOutput (git_credentials_test.go)
// cannot stand in for this. It drives `rev-parse <token>`, and MEASURED that
// command puts the token on BOTH streams — stdout 20 bytes, stderr 200 bytes —
// so it stays green against a fix that redacts only one half. Splitting the
// streams creates two independently-redactable paths, so each needs a fixture
// that exercises exactly one of them.
//
// This command exits 0 with the token on stdout and NOTHING on stderr, so only
// the success path's redactToken can save it. `remote get-url origin` is the
// production shape of the same thing: it feeds GitStatusResult.RemoteURL, which
// carries a json tag and goes out on the wire.
func TestGitCommandWithCreds_RedactsTokenOnStdoutOnly(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	dir := vwi7Repo(t, false)

	out, err := svc.gitCommandWithCreds(dir, testGitUser, testGitToken,
		"log", "-1", "--format=tformat:"+testGitToken)
	if err != nil {
		t.Fatalf("precondition: this command must EXIT 0 with the token on stdout, got %v", err)
	}
	if strings.Contains(out, testGitToken) {
		t.Errorf("the value returned on the SUCCESS path leaks the token: %q", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("expected the token replaced by the placeholder on the success path, got %q", out)
	}
}

// TestGitCommandWithCreds_RedactsTokenOnStderrOnly is the stderr half, and it
// also pins the acceptance criterion that the error path still CARRIES git's
// stderr.
//
// MEASURED: `cat-file -p <token>` exits 128 with stdout EMPTY (0 bytes) and the
// token echoed on stderr inside `fatal: Not a valid object name <token>`. So
// this fixture cannot be satisfied by redacting stdout, and the diagnostic
// assertion cannot be satisfied by an error that drops stderr — which is the
// regression the split could most easily introduce, and the one that would make
// every git failure in the product undiagnosable.
//
// A negative result recorded so it is not re-derived: an https remote URL
// carrying the token is NOT a usable fixture here. git 2.47.3 strips userinfo
// from the URL in its own diagnostics — `ls-remote` against
// https://user:<token>@example.invalid/x.git fails with
// `fatal: unable to access 'https://example.invalid/x.git/'`, no token present.
// The reasoning that git echoes the credential back is wrong for this git.
func TestGitCommandWithCreds_RedactsTokenOnStderrOnly(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	dir := vwi7Repo(t, false)

	out, err := svc.gitCommandWithCreds(dir, testGitUser, testGitToken, "cat-file", "-p", testGitToken)
	if err == nil {
		t.Fatalf("precondition: cat-file -p on a bogus object name must fail; got out=%q", out)
	}
	msg := err.Error()
	if strings.Contains(msg, testGitToken) {
		t.Errorf("the error from the split ERROR path leaks the token: %v", err)
	}
	if !strings.Contains(msg, "***") {
		t.Errorf("expected the token replaced by the placeholder in the error, got: %v", err)
	}
	// The diagnostic itself must survive the split. Without this the test is
	// satisfied by an error that carries no stderr at all, which redacts the
	// token by discarding the only thing that makes a git failure diagnosable.
	if !strings.Contains(msg, "Not a valid object name") {
		t.Errorf("the error no longer names git's own diagnostic, so a failure is undiagnosable: %v", err)
	}
}

// TestGetStatus_UnreadableAheadBehindCountIsAnError pins the tightened
// `len(parts) == 2` guard.
//
// With stdout isolated, a successful `rev-list --left-right --count` emits
// exactly two integers, so a different field count means git answered something
// that cannot be read. Previously the block was silently SKIPPED and ahead/behind
// kept their zeros — a value measured against nothing, which GitStatus.tsx draws
// as "up to date". That was already inconsistent with the two strconv.Atoi
// failures eight lines below, which hard-fail the request: the function refused
// the request for an unreadable FIELD while swallowing a missing one.
//
// The fault is injected with a `git` wrapper on PATH that rewrites the output of
// this ONE subcommand and execs the real binary for every other call, so the
// rest of getStatusCLI runs untouched. A fixture built out of real repository
// state is not available here: MEASURED, rev-list answers two fields at exit 0
// even under the ambiguous-refname fault, which is precisely why that fault must
// NOT reach this guard and is asserted not to in the test above.
func TestGetStatus_UnreadableAheadBehindCountIsAnError(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)
	dir := vwi7AheadByOne(t, false)

	// CONTROL first, through the real git: this fixture reaches the rev-list
	// block and answers 1/0. If it did not, the arm below would pass for the
	// trivial reason that the block never runs.
	st, err := svc.GetStatus(dir)
	if err != nil {
		t.Fatalf("control: GetStatus returned err=%v before any wrapper was installed", err)
	}
	if st.Ahead != 1 {
		t.Fatalf("control: ahead=%d, want 1 — the fixture does not reach the rev-list block", st.Ahead)
	}

	vwi7InterceptRevListCount(t, "ONEFIELD")

	st, err = svc.GetStatus(dir)
	if err == nil {
		t.Errorf("rev-list answered an unreadable field count and GetStatus returned err=nil with ahead=%d behind=%d trackingBranch=%q — 0/0 here is a count measured against nothing, drawn as \"up to date\"",
			st.Ahead, st.Behind, st.TrackingBranch)
	}
}

// vwi7InterceptRevListCount puts a `git` shim first on PATH for the rest of the
// test. The shim replaces the stdout of `rev-list --left-right --count` with
// replacement at exit 0 and execs the real git for everything else.
func vwi7InterceptRevListCount(t *testing.T, replacement string) {
	t.Helper()
	realGit := vwi7RealGit(t)
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--left-right\" ]; then\n" +
		"    printf '%s\\n' '" + replacement + "'\n" +
		"    exit 0\n" +
		"  fi\n" +
		"done\n" +
		"exec " + realGit + " \"$@\"\n"
	//nolint:gosec // a PATH shim must be executable to be exec'd at all; written into this test's own t.TempDir() under a fixed name, never user input
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func vwi7RealGit(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	return p
}
