package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-x2st. pullCLI's `git diff --name-only` had no fault arm, and its
// failure was indistinguishable from success.
//
// git.go:283-289 initialises changedFiles to []string{} and overwrites it only
// when the diff succeeds. That block runs ONLY when previousCommit !=
// currentCommit, so "no files changed" is the one answer that cannot be correct
// inside it — yet a failed diff produces exactly that answer, and nothing is
// logged at any level. PullVerified:438 then early-returns truth.Success on
// len(ChangedFiles) == 0, skipping the redeploy, and stackFilesChanged:494-501
// independently returns false for every stack on an empty list. The stack keeps
// running the OLD compose content while the worktree sits on the NEW commit,
// and the operator is told it worked.
//
// HOW THE FAULT IS INJECTED, and why no seam was needed. Real git, throughout.
// A post-merge hook in the working clone deletes the LOOSE OBJECT for
// ORIG_HEAD, i.e. the very commit pullCLI captured as previousCommit before the
// pull. Everything pullCLI does before the diff still succeeds — status is
// clean, the pull fast-forwards, HEAD reads back — and only `git diff
// --name-only <PREV> <CUR>` fails, with `fatal: bad object <PREV>` and exit 128.
// That is the narrowest possible injection: it breaks the one command under
// test and no other.
//
// THREE PRECONDITIONS, EACH ASSERTED IN-ARM RATHER THAN ASSUMED, because each
// one failing would hand back a fixture that quietly does nothing and a test
// that passes for the wrong reason:
//
//  1. The object must be LOOSE. A packed object makes the hook's rm a silent
//     no-op. OBSERVED (git 2.47.3): a local-path `git clone` of a bare origin
//     leaves objects loose and .git/objects/pack empty, because git's local
//     clone path hardlinks objects rather than negotiating a pack —
//     `git count-objects -v` in such a clone reports `count: 3, in-pack: 0,
//     packs: 0`. So the plain clone this fixture shares with
//     git_faultreach_test.go:299 is sufficient. assertLooseObject pins that,
//     since the day it stops being true is the day this test starts passing
//     vacuously; see its own comment for why the assertion must be made
//     against the CLONE's path and not the origin's.
//  2. post-merge's exit status must be ignored by git. It is documented as
//     ignored, and the hook here ends `exit 1` to exercise that; VERIFIED, not
//     cited — `git pull --ff-only` exits 0 with the hook installed. If a
//     non-zero hook failed the pull, the fixture would exercise pullFailure
//     instead and prove nothing about the diff.
//  3. The ref must actually MOVE. Asserted in BOTH arms, not just the fault
//     arm. A control whose pull silently did nothing returns `git diff PREV
//     CUR` = exit 0 with EMPTY output, which is indistinguishable from a
//     working control at a glance. That happened while building this fixture;
//     only the ref-moved assertion caught it.
//
// gitCommandWithCreds:227-235 TrimSpaces both return paths, so the existing
// `diffOutput != ""` check already excludes the Split -> [""] case. The defect
// is the discarded error, not the empty-string handling.

// The two post-merge hooks below are the fault injectors. Both run with cwd at
// the top of the worktree, which is why their paths are relative, and both end
// `exit 1` to exercise the documented-and-VERIFIED fact that git ignores a
// post-merge hook's exit status: with either installed, `git pull --ff-only`
// still exits 0. If it did not, the fixture would divert into pullFailure and
// prove nothing about the code after the pull.

// hookDeleteOrigHeadObject breaks `git diff --name-only <PREV> <CUR>` and
// nothing else, by deleting the loose object for the commit pullCLI captured as
// previousCommit. OBSERVED: `fatal: bad object <PREV>`, exit 128.
const hookDeleteOrigHeadObject = "#!/bin/sh\n" +
	"P=$(git rev-parse ORIG_HEAD) || exit 1\n" +
	"rm -f \".git/objects/$(echo \"$P\" | cut -c1-2)/$(echo \"$P\" | cut -c3-)\"\n" +
	"exit 1\n"

// hookBreakHead breaks the post-pull `git rev-parse HEAD` and nothing before
// it, by repointing .git/HEAD at a branch that does not exist. OBSERVED:
// `fatal: ambiguous argument 'HEAD'`, exit 128, while status, the pre-pull HEAD
// read and the pull itself all exit 0.
const hookBreakHead = "#!/bin/sh\n" +
	"printf 'ref: refs/heads/gone\\n' > .git/HEAD\n" +
	"exit 1\n"

// pullDiffFixture builds a bare origin, a seed clone that pushes a second
// commit, and a working clone positioned one commit behind it, installing
// hookScript as post-merge when it is non-empty. Passing "" is the CONTROL: the
// same fixture with nothing broken, which is what makes each fault arm
// diagnostic rather than merely red. It returns the working clone and the
// commit the pull will move away from.
func pullDiffFixture(t *testing.T, hookScript string) (workDir, previousCommit string) {
	t.Helper()

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

	previousCommit = gitOutput(t, work, "rev-parse", "HEAD")
	assertLooseObject(t, work, previousCommit)

	if hookScript != "" {
		path := filepath.Join(work, ".git", "hooks", "post-merge")
		//nolint:gosec // hook under the test's own t.TempDir(); must be executable to run
		if err := os.WriteFile(path, []byte(hookScript), 0o755); err != nil {
			t.Fatalf("write post-merge hook: %v", err)
		}
	}

	return work, previousCommit
}

// assertLooseObject fails unless commit's object file is loose in dir, so a
// packed object is reported as a broken fixture rather than passing as a clean
// tree. See precondition 1 above.
//
// dir MUST be the CLONE, never the bare origin, and the hook deletes the
// CLONE's entry for the same reason. A local-path `git clone` HARDLINKS objects
// out of the origin rather than copying them, so the two paths are one file
// with two names: OBSERVED, `stat -c 'inode=%i nlink=%h'` on both gives
// `inode=5375248 nlink=2` for the same object. `rm` in the clone therefore
// unlinks only the clone's name — after it, `git cat-file -t <commit>` in the
// clone gives `fatal: git cat-file: could not get object info` while the same
// command in the origin still answers `commit`, which is exactly the asymmetry
// this fixture needs.
//
// Two plausible "tidies" would silently disarm it, and both leave the test
// green while it tests nothing: asserting against the ORIGIN's path (which
// stays populated no matter what the hook does), or cloning with
// --no-hardlinks (which is fine on its own, but invites the first mistake by
// making the two trees look independent). The pull must also never re-fetch the
// deleted object, or it would simply come back.
func assertLooseObject(t *testing.T, dir, commit string) {
	t.Helper()
	path := filepath.Join(dir, ".git", "objects", commit[:2], commit[2:])
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("loose object for %s not at %s (%v): the object is packed, so the hook's rm is a silent no-op and this fixture injects no fault at all", commit, path, err)
	}
}

// assertRefMoved fails unless the pull actually fast-forwarded. Every assertion
// downstream is vacuous without it: git.go:284 gates the diff on the two
// commits differing, so a pull that did nothing never reaches the site.
func assertRefMoved(t *testing.T, ar truth.ActionResult, previous, current string) {
	t.Helper()
	if ar.Outcome == truth.OutcomeNoChange {
		t.Fatalf("outcome = %q: the pull did not advance HEAD, so the diff at git.go:285 was never reached and this fixture proves nothing", ar.Outcome)
	}
	if previous == current {
		t.Fatalf("previousCommit == currentCommit == %q: git.go:284 gates the diff on these differing, so the site under test never ran", current)
	}
}

// TestPullVerified_DiffFaultIsPartialNotSuccess pins git.go:285.
//
// redeploy is false and docker is nil deliberately, because that is the WEAKEST
// place to report the fault and therefore the right place to pin it: the diff in
// pullCLI runs unconditionally,
// whether or not a redeploy was requested, so its failure is unconditional too.
// What git.go:438 would otherwise emit is truth.KV("changedFiles", []) — a
// positive claim that nothing changed, on a ref that demonstrably moved. That
// claim is false no matter what the redeploy flag says, which is why the Partial
// does not depend on it.
func TestPullVerified_DiffFaultIsPartialNotSuccess(t *testing.T) {
	work, previousCommit := pullDiffFixture(t, hookDeleteOrigHeadObject)

	svc := NewGitService(&config.Config{}, nil)
	ar, pullResult := svc.PullVerified(work, false, nil)

	if pullResult == nil {
		t.Fatal("pullResult is nil; the pull itself succeeded, so its commits are still the caller's answer")
	}
	assertRefMoved(t, ar, pullResult.PreviousCommit, pullResult.CurrentCommit)
	if pullResult.PreviousCommit != previousCommit {
		t.Errorf("previousCommit = %q, want %q — the fixture and the code disagree about where the pull started", pullResult.PreviousCommit, previousCommit)
	}

	if ar.Outcome != truth.OutcomePartial {
		t.Fatalf("outcome = %q (reason %q, changedFiles %v), want %q — the diff failed, so the changed-file list is unknown, and reporting an unknown list as success skips the redeploy while telling the operator it worked",
			ar.Outcome, ar.Reason, pullResult.ChangedFiles, truth.OutcomePartial)
	}
	if !strings.Contains(ar.Reason, "diff") {
		t.Errorf("reason = %q, want it to name the diff — the operator cannot act on a partial that does not say which read failed", ar.Reason)
	}
	if diffErr, ok := ar.Details["diffError"]; !ok || diffErr == "" {
		t.Errorf("details[diffError] = %v, want the cause of the failed diff", ar.Details["diffError"])
	}
	if pullResult.DiffError == "" {
		t.Error("PullResult.DiffError is empty; it is the only thing that separates a failed diff from a genuinely empty one, and both consumers branch on the empty list")
	}
	if len(pullResult.ChangedFiles) != 0 {
		t.Errorf("changedFiles = %v, want empty — the diff failed, so there is no list to report", pullResult.ChangedFiles)
	}
}

// TestPullVerified_DiffSucceedsIsSuccessWithChangedFiles is the control arm for
// the test above: the identical fixture with NO hook, on the same instrument.
//
// Without it the fault arm proves only that something in the fixture broke, not
// that the hook broke the diff specifically. This arm has to reach the same site
// and come out the other way — a populated ChangedFiles, an empty DiffError, and
// the ordinary success outcome.
func TestPullVerified_DiffSucceedsIsSuccessWithChangedFiles(t *testing.T) {
	work, previousCommit := pullDiffFixture(t, "")

	svc := NewGitService(&config.Config{}, nil)
	ar, pullResult := svc.PullVerified(work, false, nil)

	if pullResult == nil {
		t.Fatal("pullResult is nil; the control pull was supposed to succeed outright")
	}
	assertRefMoved(t, ar, pullResult.PreviousCommit, pullResult.CurrentCommit)
	if pullResult.PreviousCommit != previousCommit {
		t.Errorf("previousCommit = %q, want %q", pullResult.PreviousCommit, previousCommit)
	}

	if ar.Outcome != truth.OutcomeSuccess {
		t.Fatalf("outcome = %q (reason %q), want %q — nothing was broken in this arm, so a non-success outcome means the fixture itself is faulty and the fault arm proves nothing",
			ar.Outcome, ar.Reason, truth.OutcomeSuccess)
	}
	if pullResult.DiffError != "" {
		t.Errorf("DiffError = %q, want empty — the diff succeeded here", pullResult.DiffError)
	}
	if len(pullResult.ChangedFiles) == 0 {
		t.Fatal("changedFiles is empty; the second commit rewrote docker-compose.yml, so an empty list here means the diff never ran and the fault arm's empty list is not diagnostic")
	}
	if pullResult.ChangedFiles[0] != "docker-compose.yml" {
		t.Errorf("changedFiles = %v, want [docker-compose.yml]", pullResult.ChangedFiles)
	}
	if _, ok := ar.Details["diffError"]; ok {
		t.Errorf("details[diffError] = %v, want it absent on a diff that succeeded", ar.Details["diffError"])
	}
}

// TestPullVerified_HeadReadFaultAfterPullIsFailedNotSuccess pins git.go:281.
//
// The sibling of the diff fault, five lines above it and a stronger form of the
// same class: the post-pull `rev-parse HEAD` discarded its error outright, while
// the IDENTICAL command reading previousCommit checks its own. Two adjacent
// reads of HEAD, opposite handling.
//
// What the discard produced: currentCommit == "", so previousCommit !=
// currentCommit is TRUE, so headAdvanced is TRUE, and PullVerified reported
// SUCCESS naming an empty currentCommit — while the diff, ChangedFiles and the
// redeploy decision were all derived from a commit that was never read.
//
// The injection breaks only the post-pull read: `status --porcelain`, the
// pre-pull HEAD read and `pull --ff-only` all still exit 0 (OBSERVED), and only
// the read AFTER the merge hits the repointed HEAD.
//
// The control arm is TestPullVerified_DiffSucceedsIsSuccessWithChangedFiles,
// which runs this same fixture with hookScript "" and gets Success. Both faults
// share one control because they share one fixture.
func TestPullVerified_HeadReadFaultAfterPullIsFailedNotSuccess(t *testing.T) {
	work, previousCommit := pullDiffFixture(t, hookBreakHead)

	svc := NewGitService(&config.Config{}, nil)
	ar, pullResult := svc.PullVerified(work, false, nil)

	// Reach proof, and it cannot come from the ActionResult here: pullCLI
	// returns an error, so there is no PullResult to read the commits from. The
	// branch ref is the evidence that the pull genuinely landed and that only
	// the HEAD read broke — otherwise a fixture that failed BEFORE the pull
	// would produce the same Failed outcome for an entirely different reason.
	if moved := gitOutput(t, work, "rev-parse", "refs/heads/main"); moved == previousCommit {
		t.Fatalf("refs/heads/main is still %q: the pull did not land, so this fixture is failing before git.go:281 and proves nothing about it", moved)
	}

	if ar.Outcome != truth.OutcomeFailed {
		t.Fatalf("outcome = %q (reason %q, pullResult %+v), want %q — HEAD could not be read after the pull, so currentCommit is empty and every value derived from it is meaningless",
			ar.Outcome, ar.Reason, pullResult, truth.OutcomeFailed)
	}
	if ar.Err == nil {
		t.Fatal("ActionResult.Err is nil; the cause is the only thing that tells the operator what to fix")
	}
	if !strings.Contains(ar.Err.Error(), "failed to get HEAD after pull") {
		t.Errorf("err = %q, want it to name the post-pull HEAD read — an unreadable HEAD and a failed diff are different faults and must not be reported as the same one", ar.Err.Error())
	}
	if pullResult != nil {
		t.Errorf("pullResult = %+v, want nil — currentCommit was never read, so every field derived from it is meaningless", pullResult)
	}
}
