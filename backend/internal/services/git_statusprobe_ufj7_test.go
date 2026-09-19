package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// dirtyRepo builds a repository with one committed, then modified, TRACKED file,
// so `git status --porcelain` has exactly one line to report.
//
// The file must be tracked-and-modified, not merely untracked. DirtyCount == 1
// cannot tell the two fixtures apart, so an untracked-file "simplification" here
// would pass every assertion in this test while silently moving the control onto
// untracked-file reporting — a different git behaviour, and one that the
// status.showUntrackedFiles fault vector interacts with.
func dirtyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// -b main explicitly: `git init` alone takes the branch name from the ambient
	// init.defaultBranch, so a CI host configured for "master" would leave the
	// origin/main tracking ref in the ahead/behind test naming a branch that does
	// not exist, and that arm would silently stop reaching the rev-list block.
	mustGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\nmodified\n"), 0o600); err != nil {
		t.Fatalf("modify f.txt: %v", err)
	}
	return dir
}

// TestGetStatus_ProbeFailureIsNotReportedAsClean pins agent-os-ufj7.
//
// getStatusCLI softened the `git status --porcelain` error to `err == nil`, so
// any failure of that one call left dirty=false and dirtyCount=0 while both
// rev-parse calls above it had already succeeded. handlers/git.go then emitted
// `"dirty": false` inside a 200 next to a valid branch and commit, and nothing
// on the wire distinguished that from a genuinely clean worktree. That is a
// WRONG value where the convention stated four times at git.go:130-133 promises
// an absent one.
//
// The fault is a TRUNCATED INDEX and not a chmod, deliberately. The server runs
// as root in the production container, where root bypasses a permission bit, so
// a chmod-based fixture would report "cannot reproduce" in the environment the
// defect actually ships in. A truncated index is a CONTENT fault: root cannot
// read past the end of a 4-byte file either.
//
// The two arms run on the SAME instrument and must come out DIFFERENTLY. Arm 1
// alone is satisfied by an implementation that errors on every repository; the
// control is what makes the pair discriminate.
func TestGetStatus_ProbeFailureIsNotReportedAsClean(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// ARM 1 — the CONTROL, and it must fire. An undamaged dirty repository is
	// reported as dirty. Without this, arm 2's "not clean" could be produced by
	// an instrument that never sees a dirty tree at all.
	clean := dirtyRepo(t)
	st, err := svc.GetStatus(clean)
	if err != nil {
		t.Fatalf("control: GetStatus on an undamaged dirty repo returned err=%v; the fixture is broken, not the code", err)
	}
	if !st.Dirty || st.DirtyCount != 1 {
		t.Fatalf("control: GetStatus reported dirty=%v count=%d, want dirty=true count=1; the fixture never made the repo dirty", st.Dirty, st.DirtyCount)
	}

	// ARM 2 — the defect. Same repository shape, index truncated so that
	// `status --porcelain` exits 128 while both rev-parse calls still exit 0.
	broken := dirtyRepo(t)
	// ANY four bytes reproduce this. The magic "DIRC" is NOT load-bearing: at this
	// size git's length check runs before its signature check, so four bytes of
	// random data produce the identical `index file smaller than expected` fault.
	// MEASURED on git 2.47.3. Do not "restore" the signature bytes believing they
	// matter — the invariant is the LENGTH, not the magic.
	if err := os.WriteFile(filepath.Join(broken, ".git", "index"), []byte("DIRC"), 0o600); err != nil {
		t.Fatalf("truncate index: %v", err)
	}
	// Precondition: the fault must leave both rev-parse calls intact, or this test
	// silently measures the no-repository path instead of the status path and goes
	// green for the wrong reason.
	//
	// Probed through gitCommandWithCreds, NOT through mustGit, on purpose. mustGit
	// shells out to an ambient git; the code under test pins its child with LC_ALL=C
	// and a cleared LANGUAGE (see gitCmdWithCreds in git_credentials.go). Asserting
	// the precondition on the ambient git would prove the fault spares a DIFFERENT
	// git process than the one the assertion below exercises.
	if _, err := svc.gitCommandWithCreds(broken, "", "", "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		t.Fatalf("precondition: rev-parse --abbrev-ref HEAD must still succeed under the fault, got %v", err)
	}
	if _, err := svc.gitCommandWithCreds(broken, "", "", "rev-parse", "HEAD"); err != nil {
		t.Fatalf("precondition: rev-parse HEAD must still succeed under the fault, got %v", err)
	}

	st, err = svc.GetStatus(broken)
	if err == nil {
		t.Fatalf("status probe failed but GetStatus returned err=nil with dirty=%v dirtyCount=%d — "+
			"a WRONG value; the caller cannot tell this from a genuinely clean worktree", st.Dirty, st.DirtyCount)
	}
}

// TestGetStatus_AheadBehindProbeFailureIsNotReportedAsUpToDate pins the SECOND
// site of agent-os-ufj7's class, found by the pre-implementation adversary pass
// and roughly 60 lines below the first, in this same function.
//
// The rev-list call that counts ahead/behind was softened the same way, and its
// //geterrors:ignore gave a reason that is factually wrong about its own
// position: "with no usable upstream, trackingBranch is empty and both counts
// stay 0". That describes the OUTER guard, `if trackingBranch != ""`. INSIDE
// that guard trackingBranch is non-empty by construction — a usable tracking
// ref exists, named either by @{upstream} or by the origin/<branch> fallback —
// so "no usable upstream" is the one condition that CANNOT reach the rev-list.
// What actually reaches it is a rev-list that failed WITH a valid tracking ref,
// and that left ahead=0/behind=0, which GitStatus.tsx gates with
// `{gitStatus.ahead > 0 && ...}` and therefore draws as "up to date".
//
// The block was already inconsistent with itself: eight lines below the
// softened call, an UNPARSEABLE count hard-fails the whole request with
// `failed to parse behind count`. So the function refused the request for a
// weaker fault than the one it swallowed.
//
// FAULT: a remote-tracking ref pointing at a valid object that is not a commit
// (a blob). MEASURED on git 2.47.3: `rev-parse --verify --quiet` resolves it
// (exit 0), so trackingBranch IS set and the guarded block IS entered; `status
// --porcelain` and both rev-parse HEAD calls stay at exit 0; and only rev-list
// fails, with `object ... is a blob, not a commit`, exit 128. A content fault,
// so root does not bypass it.
func TestGetStatus_AheadBehindProbeFailureIsNotReportedAsUpToDate(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	// ARM 1 — the CONTROL, and it must fire. A tracking ref pointing at a real
	// COMMIT counts cleanly, proving the fixture reaches the rev-list block at
	// all. Without it, arm 2's error could come from never entering the guard.
	ok := dirtyRepo(t)
	headSHA := strings.TrimSpace(mustGitOut(t, ok, "rev-parse", "HEAD"))
	writeRemoteRef(t, ok, headSHA)
	st, err := svc.GetStatus(ok)
	if err != nil {
		t.Fatalf("control: GetStatus with a commit-valued tracking ref returned err=%v; the fixture is broken, not the code", err)
	}
	if st.TrackingBranch != "origin/main" {
		t.Fatalf("control: trackingBranch=%q, want %q — the fixture never entered the rev-list block, so arm 2 would prove nothing", st.TrackingBranch, "origin/main")
	}
	if st.Ahead != 0 || st.Behind != 0 {
		t.Fatalf("control: ahead=%d behind=%d, want 0/0 for a ref at HEAD", st.Ahead, st.Behind)
	}

	// ARM 2 — the defect. Same shape, but the tracking ref names a BLOB, so
	// rev-list fails while every other probe in the function succeeds.
	broken := dirtyRepo(t)
	blob := strings.TrimSpace(mustGitOut(t, broken, "hash-object", "-w", "f.txt"))
	writeRemoteRef(t, broken, blob)

	// Preconditions, through the same call path as the code under test: the
	// fault must leave status and both rev-parse calls intact, or this arm is
	// measuring some other failure.
	if _, err := svc.gitCommandWithCreds(broken, "", "", "status", "--porcelain"); err != nil {
		t.Fatalf("precondition: status --porcelain must still succeed under the fault, got %v", err)
	}
	if _, err := svc.gitCommandWithCreds(broken, "", "", "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main"); err != nil {
		t.Fatalf("precondition: the tracking ref must still resolve, or trackingBranch stays empty and the rev-list block is never entered: %v", err)
	}

	st, err = svc.GetStatus(broken)
	if err == nil {
		t.Fatalf("rev-list failed but GetStatus returned err=nil with ahead=%d behind=%d trackingBranch=%q — "+
			"0/0 is drawn as \"up to date\", a WRONG value the caller cannot tell from a genuinely synced repository",
			st.Ahead, st.Behind, st.TrackingBranch)
	}
}

// mustGitOut is mustGit's value-returning sibling: the preconditions above need
// the command's OUTPUT, which mustGit discards.
func mustGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // test helper, explicit argv, not a shell string
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return string(out)
}

// writeRemoteRef plants refs/remotes/origin/main directly. `git update-ref`
// refuses a ref whose object is missing, and refuses nothing about a blob — but
// writing the file is the one route that works for BOTH arms with one helper.
func writeRemoteRef(t *testing.T, dir, sha string) {
	t.Helper()
	refDir := filepath.Join(dir, ".git", "refs", "remotes", "origin")
	if err := os.MkdirAll(refDir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", refDir, err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "main"), []byte(sha+"\n"), 0o600); err != nil {
		t.Fatalf("write remote ref: %v", err)
	}
}
