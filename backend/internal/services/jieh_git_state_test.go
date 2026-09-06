package services

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-jieh. buildDirectoryRecord answered FOUR different questions with
// one empty GitBranch, and both screens render an empty branch as an em dash.
// An operator seeing that em dash could not tell a detached checkout (fine,
// deliberate, ignore it) from a scan that failed (go look at the box), and
// those need opposite responses.
//
// The four, as they were on c8c5c6b (scanner.go:980-992):
//
//   - HEAD holds a bare SHA (detached) -> the "ref: refs/heads/" prefix test
//     fails and gitBranch stays "";
//   - HEAD unreadable -> os.ReadFile's error is discarded;
//   - .git is a FILE holding "gitdir: <path>" (a linked worktree or a
//     submodule checkout) -> os.Stat succeeds so isGitRepo is true, but
//     .git/HEAD is not a path, so the read fails ENOTDIR and is discarded;
//   - os.Stat(.git) fails for a reason OTHER than not-exist -> :981 asserted
//     "this is not a git repo" on a fault. os.IsNotExist is the only stat
//     answer that means absent; every other one means "could not find out".
//     Same shape as agent-os-d5ff, which swept os.Stat sites and never
//     dispositioned this one: `git show add3405 | command grep -c isGitRepo`
//     is 0.
//
// THE FIXTURES ARE NOT chmod. `chmod 000` is a no-op for root
// (CAP_DAC_OVERRIDE), so a red arm built on it does not arm wherever the suite
// runs as root. ELOOP and EISDIR are structural properties of the path,
// resolved before any permission check, so no uid and no capability can defeat
// them. Measured on this box (uid=1000) with a standalone `go run`, all arms on
// one instrument:
//
//	ELOOP    stat(.git)        err=too many levels of symbolic links  IsNotExist=false
//	EISDIR   ReadFile(.git/HEAD) err=is a directory                   IsNotExist=false
//	ENOTDIR  ReadFile(.git/HEAD) err=not a directory                  IsNotExist=false  (.git is a file)
//	ENOENT   stat(.git)        err=no such file or directory          IsNotExist=true
//
// Every assertion below compares the WHOLE rendered string, never a prefix or a
// substring: `strings.Contains(branch, "detached")` is satisfied by a branch
// literally named "detached-fix", which is exactly the collapse being fixed.

// jiehUnknown is the user-visible string for "the scan could not read this
// repository's state". It is written out as a literal here rather than
// referencing services.gitStateUnknown on purpose: a test that asserts a
// constant equals itself passes no matter what the constant says, and this
// string reaches an operator's screen.
//
// It contains a SPACE, which git-check-ref-format forbids in a ref name, so it
// can never be mistaken for a real branch.
const jiehUnknown = "unknown (read failed)"

// jiehScanner builds a ScannerService with one configured root. The db handle
// is only needed by the ScanAll path at the bottom of this file; the
// buildDirectoryRecord tests never touch it.
func jiehScanner(t *testing.T, root string) *ScannerService {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("opening the in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewScannerService(&config.Config{StacksDir: root}, db)
}

// jiehCaptureSlog redirects the default logger into a buffer for one test, so
// the fault arms can assert that the discarded error is now REPORTED and not
// merely turned into a different silent value.
//
// Deliberately its own helper rather than the captureSlog in
// git_credentials_test.go: this file lands on a parallel branch and a shared
// helper across two in-flight branches is a merge conflict waiting to happen.
func jiehCaptureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// jiehRepoDir seeds <parent>/<name> as a directory containing a .git DIRECTORY
// whose HEAD holds headContent verbatim, and returns the repo path.
func jiehRepoDir(t *testing.T, parent, name, headContent string) string {
	t.Helper()
	repo := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("seeding the repo fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte(headContent), 0o644); err != nil {
		t.Fatalf("seeding HEAD: %v", err)
	}
	return repo
}

// ---------------------------------------------------------------------------
// The two arms that must NOT move: a plain branch, and no repository at all.
// Without these, every assertion below is satisfied by a fix that answers
// "unknown (read failed)" to absolutely everything.
// ---------------------------------------------------------------------------

func TestBuildDirectoryRecord_AttachedBranchIsUnchanged(t *testing.T) {
	root := t.TempDir()
	repo := jiehRepoDir(t, root, "web", "ref: refs/heads/release\n")

	rec := jiehScanner(t, root).buildDirectoryRecord(repo, root)

	if !rec.isGitRepo {
		t.Fatalf("isGitRepo = false for a plain git repo; want true")
	}
	if rec.gitBranch != "release" {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, "release")
	}
	if rec.directory.GitBranch != rec.gitBranch {
		t.Fatalf("models.Directory.GitBranch = %q but record gitBranch = %q; the two must not drift",
			rec.directory.GitBranch, rec.gitBranch)
	}
}

func TestBuildDirectoryRecord_NoGitDirIsNotARepo(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("seeding the plain-directory fixture: %v", err)
	}

	rec := jiehScanner(t, root).buildDirectoryRecord(plain, root)

	// ENOENT is the ONE stat answer that means absent, and it must keep
	// meaning that. A fix that answered "unknown" here would light a git badge
	// on every non-git directory Capstan monitors.
	if rec.isGitRepo {
		t.Fatalf("isGitRepo = true for a directory with no .git; want false")
	}
	if rec.gitBranch != "" {
		t.Fatalf("gitBranch = %q for a non-repo; want the empty string", rec.gitBranch)
	}
}

// ---------------------------------------------------------------------------
// State 1: detached HEAD. Edwin's decision: `detached@<short sha>`.
// ---------------------------------------------------------------------------

func TestBuildDirectoryRecord_DetachedHeadNamesTheCommit(t *testing.T) {
	root := t.TempDir()
	const sha = "abc1234def5678901234567890abcdef12345678"
	repo := jiehRepoDir(t, root, "web", sha+"\n")

	rec := jiehScanner(t, root).buildDirectoryRecord(repo, root)

	if !rec.isGitRepo {
		t.Fatalf("isGitRepo = false for a detached checkout; want true")
	}
	// The WHOLE string, not a prefix: a branch named "detached-hotfix" would
	// satisfy any Contains("detached") assertion.
	if rec.gitBranch != "detached@abc1234" {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, "detached@abc1234")
	}
}

// git can be configured with SHA-256 object names, which are 64 hex characters.
// The short form is still the first 7.
func TestBuildDirectoryRecord_DetachedHeadSha256(t *testing.T) {
	root := t.TempDir()
	sha := strings.Repeat("0f", 32) // 64 hex chars
	repo := jiehRepoDir(t, root, "web", sha+"\n")

	rec := jiehScanner(t, root).buildDirectoryRecord(repo, root)

	if rec.gitBranch != "detached@0f0f0f0" {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, "detached@0f0f0f0")
	}
}

// A detached HEAD is a legitimate state, not a fault, so nothing is logged for
// it. This is the arm that stops the fix from being "warn about everything",
// which would bury the two real faults below in noise.
func TestBuildDirectoryRecord_DetachedHeadIsNotLoggedAsAFault(t *testing.T) {
	logs := jiehCaptureSlog(t)
	root := t.TempDir()
	repo := jiehRepoDir(t, root, "web", "abc1234def5678901234567890abcdef12345678\n")

	jiehScanner(t, root).buildDirectoryRecord(repo, root)

	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a detached HEAD logged a warning; it is a normal state, not a fault.\nlogs:\n%s", logs.String())
	}
}

// ---------------------------------------------------------------------------
// State 2: HEAD unreadable. A fault, and it must not masquerade as a git state.
// ---------------------------------------------------------------------------

func TestBuildDirectoryRecord_UnreadableHeadIsAFaultNotABlank(t *testing.T) {
	logs := jiehCaptureSlog(t)
	root := t.TempDir()
	repo := filepath.Join(root, "web")
	// EISDIR: HEAD is a DIRECTORY, so os.ReadFile opens it and then fails on
	// the read. Structural, so it fires for root too.
	if err := os.MkdirAll(filepath.Join(repo, ".git", "HEAD"), 0o755); err != nil {
		t.Fatalf("seeding the EISDIR fixture: %v", err)
	}

	rec := jiehScanner(t, root).buildDirectoryRecord(repo, root)

	if !rec.isGitRepo {
		t.Fatalf("isGitRepo = false; .git is present and readable, so this IS a repo")
	}
	if rec.gitBranch != jiehUnknown {
		t.Fatalf("gitBranch = %q; want %q. An unreadable HEAD must be distinguishable "+
			"from a detached one and from a real branch", rec.gitBranch, jiehUnknown)
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("the discarded ReadFile error is still not reported anywhere.\nlogs:\n%s", logs.String())
	}
}

// HEAD that is neither a symbolic ref into refs/heads nor an object name. The
// scanner has no idea what the branch is, and saying so is the whole point.
func TestBuildDirectoryRecord_UnparseableHeadIsUnknown(t *testing.T) {
	root := t.TempDir()
	repo := jiehRepoDir(t, root, "web", "this is not a HEAD\n")

	rec := jiehScanner(t, root).buildDirectoryRecord(repo, root)

	if rec.gitBranch != jiehUnknown {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, jiehUnknown)
	}
}

// ---------------------------------------------------------------------------
// State 3: os.Stat(.git) faults for a reason other than not-exist.
// ---------------------------------------------------------------------------

func TestBuildDirectoryRecord_StatFaultIsNotAnAbsentRepo(t *testing.T) {
	logs := jiehCaptureSlog(t)
	root := t.TempDir()
	repo := filepath.Join(root, "web")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("seeding the repo directory: %v", err)
	}
	// ELOOP: .git is a symlink to itself, so os.Stat (which follows symlinks)
	// fails with "too many levels of symbolic links". Structural, uid-blind.
	gitPath := filepath.Join(repo, ".git")
	if err := os.Symlink(gitPath, gitPath); err != nil {
		t.Fatalf("seeding the ELOOP fixture: %v", err)
	}

	rec := jiehScanner(t, root).buildDirectoryRecord(repo, root)

	// This is the assertion the pre-fix code fails: it answered "not a git
	// repo", which is a diagnosis it never established. Both screens gate the
	// branch badge on isGitRepo, so a false `false` here does not merely blank
	// the branch, it hides the failure entirely.
	if !rec.isGitRepo {
		t.Fatalf("isGitRepo = false after stat FAILED; the scan never established that this is not a repo")
	}
	if rec.gitBranch != jiehUnknown {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, jiehUnknown)
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("the discarded stat error is still not reported anywhere.\nlogs:\n%s", logs.String())
	}
}

// ---------------------------------------------------------------------------
// State 4: .git is a FILE holding "gitdir: <path>" — a linked worktree or a
// submodule checkout. This one is not a fault at all: it is a legitimate
// configuration that resolves to a real branch once the pointer is followed.
// ---------------------------------------------------------------------------

// gitrepository-layout(5): the path in a .git FILE may be relative, in which
// case it is relative to the directory CONTAINING the .git file. This is the
// spelling `git worktree add` actually writes.
func TestBuildDirectoryRecord_LinkedWorktreeRelativePointer(t *testing.T) {
	root := t.TempDir()

	realGitDir := filepath.Join(root, "main-checkout", ".git", "worktrees", "feature")
	if err := os.MkdirAll(realGitDir, 0o755); err != nil {
		t.Fatalf("seeding the linked-worktree git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"), []byte("ref: refs/heads/feature/login\n"), 0o644); err != nil {
		t.Fatalf("seeding the linked-worktree HEAD: %v", err)
	}

	wt := filepath.Join(root, "feature")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("seeding the worktree directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: ../main-checkout/.git/worktrees/feature\n"), 0o644); err != nil {
		t.Fatalf("seeding the .git pointer file: %v", err)
	}

	rec := jiehScanner(t, root).buildDirectoryRecord(wt, root)

	if !rec.isGitRepo {
		t.Fatalf("isGitRepo = false for a linked worktree; want true")
	}
	// A branch name legitimately contains '/', so the whole remainder after
	// "ref: refs/heads/" is the name.
	if rec.gitBranch != "feature/login" {
		t.Fatalf("gitBranch = %q; want %q. The gitdir: pointer was not followed", rec.gitBranch, "feature/login")
	}
}

// A submodule's .git file carries an ABSOLUTE path in some git versions and a
// relative one in others, so both spellings are exercised.
func TestBuildDirectoryRecord_SubmoduleAbsolutePointer(t *testing.T) {
	root := t.TempDir()

	realGitDir := filepath.Join(root, "super", ".git", "modules", "vendor")
	if err := os.MkdirAll(realGitDir, 0o755); err != nil {
		t.Fatalf("seeding the submodule git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("seeding the submodule HEAD: %v", err)
	}

	sub := filepath.Join(root, "super", "vendor")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("seeding the submodule working dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: "+realGitDir+"\n"), 0o644); err != nil {
		t.Fatalf("seeding the .git pointer file: %v", err)
	}

	rec := jiehScanner(t, root).buildDirectoryRecord(sub, root)

	if rec.gitBranch != "main" {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, "main")
	}
}

// A linked worktree can be detached too, and it must render the same way a
// plain detached checkout does. This is the arm that catches a fix which
// follows the pointer but then re-implements the HEAD parse differently.
func TestBuildDirectoryRecord_LinkedWorktreeDetached(t *testing.T) {
	root := t.TempDir()

	realGitDir := filepath.Join(root, "main-checkout", ".git", "worktrees", "detached")
	if err := os.MkdirAll(realGitDir, 0o755); err != nil {
		t.Fatalf("seeding the linked-worktree git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"),
		[]byte("9876543210fedcba9876543210fedcba98765432\n"), 0o644); err != nil {
		t.Fatalf("seeding the linked-worktree HEAD: %v", err)
	}

	wt := filepath.Join(root, "detached")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("seeding the worktree directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: ../main-checkout/.git/worktrees/detached\n"), 0o644); err != nil {
		t.Fatalf("seeding the .git pointer file: %v", err)
	}

	rec := jiehScanner(t, root).buildDirectoryRecord(wt, root)

	if rec.gitBranch != "detached@9876543" {
		t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, "detached@9876543")
	}
}

// A .git FILE whose contents are not a gitdir: pointer, and a pointer aimed at
// nothing. Both are faults, and neither may be reported as a branch.
func TestBuildDirectoryRecord_BrokenGitdirPointerIsAFault(t *testing.T) {
	root := t.TempDir()

	for _, tc := range []struct {
		name     string
		contents string
	}{
		{"not a pointer at all", "some other tool's marker file\n"},
		{"pointer with an empty target", "gitdir: \n"},
		{"pointer at a path that does not exist", "gitdir: ../nowhere/.git\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("seeding the fixture directory: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(tc.contents), 0o644); err != nil {
				t.Fatalf("seeding the .git file: %v", err)
			}

			rec := jiehScanner(t, root).buildDirectoryRecord(dir, root)

			if rec.gitBranch != jiehUnknown {
				t.Fatalf("gitBranch = %q; want %q", rec.gitBranch, jiehUnknown)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The sentinel's one structural property, asserted rather than asserted in a
// comment: it cannot collide with a real branch name.
// ---------------------------------------------------------------------------

func TestUnknownSentinelCannotBeARealBranchName(t *testing.T) {
	// git-check-ref-format forbids ASCII space in a ref name, so a value
	// containing one is unambiguously not a branch. `detached@<sha>` has no
	// space and IS a legal branch name; that collision is accepted
	// deliberately (Edwin chose the string) and is noted here so a later
	// reader does not mistake it for an oversight.
	if !strings.Contains(jiehUnknown, " ") {
		t.Fatalf("the unknown sentinel %q contains no space, so a repository could "+
			"legitimately have a branch by that name and the two would be indistinguishable", jiehUnknown)
	}
}

// ---------------------------------------------------------------------------
// End to end: scanner -> DB row -> what the API serves. Acceptance criterion 1
// says "scanner -> API -> both screens", and buildDirectoryRecord alone does
// not prove the value survives into stacks.git_branch, which is the field
// StackDetail.tsx:90 renders.
// ---------------------------------------------------------------------------

func TestScanAll_DetachedHeadReachesTheStackRow(t *testing.T) {
	root := t.TempDir()
	repo := jiehRepoDir(t, root, "web", "abc1234def5678901234567890abcdef12345678\n")
	if err := os.WriteFile(filepath.Join(repo, "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatalf("seeding the compose file: %v", err)
	}

	svc := jiehScanner(t, root)
	if _, err := svc.ScanAll(); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	stacks, err := svc.db.ListStacks()
	if err != nil {
		t.Fatalf("ListStacks: %v", err)
	}
	if len(stacks) != 1 {
		t.Fatalf("got %d stacks; want 1", len(stacks))
	}
	if !stacks[0].IsGitRepo {
		t.Fatalf("stack IsGitRepo = false; want true")
	}
	if stacks[0].GitBranch != "detached@abc1234" {
		t.Fatalf("stack GitBranch = %q; want %q", stacks[0].GitBranch, "detached@abc1234")
	}

	dirs, err := svc.db.ListDirectories()
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}
	found := false
	for _, d := range dirs {
		if d.Path != repo {
			continue
		}
		found = true
		if d.GitBranch != "detached@abc1234" {
			t.Fatalf("directory GitBranch = %q; want %q", d.GitBranch, "detached@abc1234")
		}
	}
	if !found {
		t.Fatalf("no directories row for %s; the end-to-end arm never reached the assertion it exists for", repo)
	}
}
