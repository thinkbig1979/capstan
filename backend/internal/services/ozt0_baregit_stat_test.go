package services

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agent-os-ozt0. bareGitBranch gated membership on os.Stat and MERGED the error
// into the value test (`if err != nil || !info.IsDir()`), so "objects/ is not
// there" and "I could not find out whether objects/ is there" produced the same
// answer -- gitLevelAbsent at the caller (scanner.go:1205-1208) -- with nothing
// logged. The walk then continues to the parent, so a bare repository whose
// objects/ or refs/ could not be stat'd is silently attributed to a DIFFERENT
// directory's git state, or to none.
//
// WHY A FAULT HERE IS RARE, which is what makes warning on it cheap rather than
// noisy. bareGitBranch is reached from ONE call site, and only down the branch
// where os.Stat(dir/.git) already returned an error satisfying os.IsNotExist
// (scanner.go:1198-1208). A dir that is itself unreadable fails THAT stat with
// EACCES, which is not IsNotExist, so it warns and returns gitLevelFault at
// :1203 and never reaches here. By the time bareGitBranch runs, dir has been
// shown to be statable. The ordinary case -- every non-git directory of every
// scan -- is a plain ENOENT on objects/, which stays silent.
//
// THE FIXTURE IS NOT chmod, for the reason d5ff_stat_fault_test.go records:
// chmod 000 is a no-op for root (CAP_DAC_OVERRIDE), so a red arm built on it
// does not arm wherever the suite runs as root. A self-referential symlink
// resolves to ELOOP before any permission check, so no uid and no capability
// can defeat it. Measured on this box (uid=1000), all five arms on one
// instrument, stat-ing <dir>/objects:
//
//	SELF-SYMLINK  err=…: too many levels of symbolic links  IsNotExist=false  IsDir=false
//	DANGLING LINK err=…: no such file or directory          IsNotExist=true   IsDir=false
//	ABSENT        err=…: no such file or directory          IsNotExist=true   IsDir=false
//	REGULAR FILE  err=<nil>                                 IsNotExist=false  IsDir=false
//	DIRECTORY     err=<nil>                                 IsNotExist=false  IsDir=true
//
// os.IsNotExist is therefore the exact discriminator, and it is the one the
// sibling site at scanner.go:1200 already uses. The REGULAR FILE row is the
// load-bearing control: it is "I read it, and the answer is no", which must
// stay silent, and it is what separates this fix from "warn whenever we return
// false".

// ozt0CaptureSlog redirects the default logger into a buffer for one test.
// Deliberately not shared with d5ffCaptureSlog: that helper carries its own
// note about cross-file coupling, and slog.SetDefault is process-global, so
// these tests must not run in parallel with anything that asserts on logs.
func ozt0CaptureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// ozt0BareTriple seeds a complete, valid bare repository layout and returns its
// directory: objects/ and refs/ as directories, HEAD naming a branch.
func ozt0BareTriple(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("seeding %s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("seeding HEAD: %v", err)
	}
	return dir
}

// ozt0Loop replaces <dir>/<name> with a self-referential symlink, so stat-ing it
// fails with ELOOP rather than ENOENT.
func ozt0Loop(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("clearing %s: %v", p, err)
	}
	if err := os.Symlink(name, p); err != nil {
		t.Fatalf("seeding the ELOOP fixture at %s: %v", p, err)
	}
}

// TestBareGitBranch_ObjectsStatFault is the RED arm. objects/ is a symlink loop,
// so it is NOT known to be absent. Pre-fix this returned ("", false) in silence,
// indistinguishable from an ordinary directory, and the walk moved to the parent
// with no record that a repository-shaped directory had been skipped.
//
// The return value deliberately does NOT change: widening membership on a fault
// would light a git badge on a directory not shown to be a repository, which is
// the very thing bareGitBranch's HEAD check exists to prevent. What changes is
// that the fault stops being invisible.
func TestBareGitBranch_ObjectsStatFault(t *testing.T) {
	logs := ozt0CaptureSlog(t)
	dir := ozt0BareTriple(t)
	ozt0Loop(t, dir, "objects")

	branch, ok := bareGitBranch(dir)

	if ok || branch != "" {
		t.Errorf("a fault on objects/ must not be reported as a located repository.\ngot (%q, %v), want (\"\", false)", branch, ok)
	}
	out := logs.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, dir) || !strings.Contains(out, "too many levels of symbolic links") {
		t.Errorf("objects/ could not be stat'd and the fault was swallowed, so a repository-shaped directory was skipped with nothing logged.\n"+
			"captured logs = %q\nwant a WARN line naming %q and \"too many levels of symbolic links\"", out, dir)
	}
}

// TestBareGitBranch_RefsStatFault covers the loop's SECOND iteration. Without
// it, a fix applied to the first subdirectory only would pass the arm above
// while leaving half the site merged.
func TestBareGitBranch_RefsStatFault(t *testing.T) {
	logs := ozt0CaptureSlog(t)
	dir := ozt0BareTriple(t)
	ozt0Loop(t, dir, "refs")

	branch, ok := bareGitBranch(dir)

	if ok || branch != "" {
		t.Errorf("a fault on refs/ must not be reported as a located repository.\ngot (%q, %v), want (\"\", false)", branch, ok)
	}
	out := logs.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, dir) || !strings.Contains(out, "too many levels of symbolic links") {
		t.Errorf("refs/ could not be stat'd and the fault was swallowed.\n"+
			"captured logs = %q\nwant a WARN line naming %q and \"too many levels of symbolic links\"", out, dir)
	}
}

// TestBareGitBranch_NotADirectory is the load-bearing control: objects/ is a
// REGULAR FILE, so it was read successfully and the answer is genuinely "this
// is not a bare repository". That is a negative finding, not a fault, and it
// must stay silent. This is the arm that separates the fix from "warn whenever
// we return false" -- a fix that merged the two would pass every other test
// here and still be the defect this bead is about, pointing the other way.
func TestBareGitBranch_NotADirectory(t *testing.T) {
	logs := ozt0CaptureSlog(t)
	dir := ozt0BareTriple(t)
	if err := os.RemoveAll(filepath.Join(dir, "objects")); err != nil {
		t.Fatalf("clearing objects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "objects"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seeding the regular-file fixture: %v", err)
	}

	branch, ok := bareGitBranch(dir)

	if ok || branch != "" {
		t.Errorf("objects/ as a regular file is not a bare repository.\ngot (%q, %v), want (\"\", false)", branch, ok)
	}
	if out := logs.String(); out != "" {
		t.Errorf("objects/ was read successfully and the answer is simply no; that is a negative finding, not a fault, and must log nothing.\ncaptured logs = %q", out)
	}
}

// TestBareGitBranch_Absent is the noise control, and it is the reason the fix
// keys on os.IsNotExist rather than on err != nil. EVERY ordinary non-git
// directory of every scan reaches this function and takes this path, so a
// warning here would be pure noise -- the objection bareGitBranch's own docblock
// raises against reporting warnings at all.
func TestBareGitBranch_Absent(t *testing.T) {
	logs := ozt0CaptureSlog(t)
	dir := t.TempDir() // an ordinary directory: no objects/, no refs/, no HEAD

	branch, ok := bareGitBranch(dir)

	if ok || branch != "" {
		t.Errorf("an ordinary directory is not a bare repository.\ngot (%q, %v), want (\"\", false)", branch, ok)
	}
	if out := logs.String(); out != "" {
		t.Errorf("an ordinary non-git directory is the normal state of almost every scanned directory and must log nothing.\ncaptured logs = %q", out)
	}
}

// TestBareGitBranch_Present is the second control: a complete, readable bare
// triple is still located, still on the right branch, and still silently.
func TestBareGitBranch_Present(t *testing.T) {
	logs := ozt0CaptureSlog(t)
	dir := ozt0BareTriple(t)

	branch, ok := bareGitBranch(dir)

	if !ok || branch != "main" {
		t.Errorf("a complete bare triple must still be located.\ngot (%q, %v), want (\"main\", true)", branch, ok)
	}
	if out := logs.String(); out != "" {
		t.Errorf("a healthy bare repository must log nothing.\ncaptured logs = %q", out)
	}
}
