package services

import (
	"os"
	"path/filepath"
	"testing"
)

// agent-os-yy00. resolveGitState used to answer "does this directory hold its
// OWN .git", which is narrower than git's own question, "is this directory
// served by a git repository". A stack nested inside a parent repo, and a bare
// repository, both answered false while `git rev-parse` answered yes for the
// same path — so six UI expressions across four components hid git affordances
// that work.
//
// These arms pin the widened predicate. They call resolveGitState directly:
// the four badge components never compute the predicate, they render a boolean
// off the wire, so the only place a regression can be caught is here.
//
// yy00Unknown is written out as a literal rather than referencing
// gitStateUnknown, for the reason jieh_git_state_test.go gives: a test that
// asserts a constant equals itself passes no matter what the constant says,
// and this string reaches an operator's screen.
const yy00Unknown = "unknown (read failed)"

// yy00WriteRepoAt seeds dir as a working-tree repository: a .git DIRECTORY
// holding HEAD verbatim.
func yy00WriteRepoAt(t *testing.T, dir, headContent string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("seeding the .git directory at %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte(headContent), 0o644); err != nil {
		t.Fatalf("seeding HEAD at %s: %v", dir, err)
	}
}

// yy00WriteBareRepoAt seeds dir as a BARE repository: git's own
// is_git_directory triple (HEAD, objects/, refs/) with no working tree and no
// .git entry of its own.
func yy00WriteBareRepoAt(t *testing.T, dir, headContent string) {
	t.Helper()
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("seeding %s under the bare repo: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte(headContent), 0o644); err != nil {
		t.Fatalf("seeding the bare repo's HEAD: %v", err)
	}
}

// yy00MkdirAll is MkdirAll with the fixture failure spelled out.
func yy00MkdirAll(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seeding the fixture directory %s: %v", dir, err)
	}
	return dir
}

// yy00RequireCleanAncestry fails the test unless EVERY directory from dir up to
// the filesystem root is free of a repository — no .git entry of any kind, and
// no bare-repository layout.
//
// The control arm below asserts "no repository anywhere above this path", and
// t.TempDir()'s ancestry is ambient: it is TMPDIR, which an operator or a CI
// image is free to place inside a checkout. Without this precondition the
// control would report a fixture problem as a code defect, or (worse) a machine
// where TMPDIR happens to sit under a repo would turn the one arm that proves
// the fix did not simply always-show into a confusing red.
func yy00RequireCleanAncestry(t *testing.T, dir string) {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}
	for p := abs; ; {
		if _, err := os.Stat(filepath.Join(p, ".git")); !os.IsNotExist(err) {
			t.Fatalf("fixture precondition violated: %s has a .git entry (stat err = %v), "+
				"so the no-repository-anywhere control cannot be built under %s", p, err, dir)
		}
		if _, err := os.Stat(filepath.Join(p, "HEAD")); err == nil {
			t.Fatalf("fixture precondition violated: %s holds a HEAD file and may read as a bare "+
				"repository, so the no-repository-anywhere control cannot be built under %s", p, dir)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return
		}
		p = parent
	}
}

// ---------------------------------------------------------------------------
// Arm 1 — the monorepo layout the bead was filed for.
// ---------------------------------------------------------------------------

func TestResolveGitState_NestedInsideParentRepoResolvesTheParentsBranch(t *testing.T) {
	root := t.TempDir()
	repo := yy00MkdirAll(t, filepath.Join(root, "monorepo"))
	yy00WriteRepoAt(t, repo, "ref: refs/heads/release\n")
	stack := yy00MkdirAll(t, filepath.Join(repo, "stacks", "web"))

	isGitRepo, branch := resolveGitState(stack)

	if !isGitRepo {
		t.Fatalf("isGitRepo = false for a directory nested inside a repository; want true")
	}
	if branch != "release" {
		t.Fatalf("branch = %q; want %q, the branch of the parent repository", branch, "release")
	}
}

// ---------------------------------------------------------------------------
// Arm 2 — a bare repository, which has no working tree and therefore no .git.
// ---------------------------------------------------------------------------

func TestResolveGitState_BareRepositoryResolvesItsBranch(t *testing.T) {
	root := t.TempDir()
	bare := yy00MkdirAll(t, filepath.Join(root, "web.git"))
	yy00WriteBareRepoAt(t, bare, "ref: refs/heads/trunk\n")

	isGitRepo, branch := resolveGitState(bare)

	if !isGitRepo {
		t.Fatalf("isGitRepo = false for a bare repository; want true")
	}
	if branch != "trunk" {
		t.Fatalf("branch = %q; want %q", branch, "trunk")
	}
}

// The bare triple alone is the weaker instrument: git additionally requires
// HEAD to be a valid symref or object name. A directory that merely happens to
// hold HEAD, objects/ and refs/ with junk in HEAD is NOT a repository, and
// without this arm the widening would light a badge on it.
func TestResolveGitState_BareTripleWithUnparseableHeadIsNotARepository(t *testing.T) {
	root := t.TempDir()
	notRepo := yy00MkdirAll(t, filepath.Join(root, "looks-like-one"))
	yy00WriteBareRepoAt(t, notRepo, "this is not a ref or an object name\n")

	isGitRepo, branch := resolveGitState(notRepo)

	if isGitRepo {
		t.Fatalf("isGitRepo = true for HEAD+objects+refs whose HEAD does not parse; want false")
	}
	if branch != "" {
		t.Fatalf("branch = %q; want the empty string", branch)
	}
}

// ---------------------------------------------------------------------------
// Arm 3 — THE CONTROL. Without this, every arm above is satisfied by a change
// that answers true to everything.
// ---------------------------------------------------------------------------

func TestResolveGitState_NoRepositoryAnywhereAboveIsNotARepo(t *testing.T) {
	root := t.TempDir()
	plain := yy00MkdirAll(t, filepath.Join(root, "a", "b", "c"))
	yy00RequireCleanAncestry(t, plain)

	isGitRepo, branch := resolveGitState(plain)

	if isGitRepo {
		t.Fatalf("isGitRepo = true for a directory with no repository at or above it; want false")
	}
	if branch != "" {
		t.Fatalf("branch = %q for a non-repo; want the empty string", branch)
	}
}

// ---------------------------------------------------------------------------
// Arm 4 — the deliberate fail-open, which is scoped to the stack's OWN .git.
// ---------------------------------------------------------------------------

// yy00LoopedGitEntry seeds <dir>/.git as a symlink loop. ELOOP is a structural
// property of the path, resolved before any permission check, so unlike
// `chmod 000` it arms whatever uid the suite runs as — including root.
func yy00LoopedGitEntry(t *testing.T, dir string) {
	t.Helper()
	gitPath := filepath.Join(dir, ".git")
	if err := os.Symlink(gitPath, gitPath); err != nil {
		t.Fatalf("seeding the self-referential .git symlink at %s: %v", gitPath, err)
	}
	if _, err := os.Stat(gitPath); err == nil || os.IsNotExist(err) {
		t.Fatalf("fixture precondition violated: stat(%s) = %v; want a non-ENOENT error", gitPath, err)
	}
}

func TestResolveGitState_OwnGitStatFaultStillFailsOpen(t *testing.T) {
	root := t.TempDir()
	stack := yy00MkdirAll(t, filepath.Join(root, "web"))
	yy00LoopedGitEntry(t, stack)

	isGitRepo, branch := resolveGitState(stack)

	// Deliberate: "there is a .git entry here that I could not read" is
	// reported as a repository with an unknown branch, so the fault is visible
	// rather than silently hidden behind a missing badge.
	if !isGitRepo {
		t.Fatalf("isGitRepo = false when the stack's own .git could not be stat'd; want true (fail-open)")
	}
	if branch != yy00Unknown {
		t.Fatalf("branch = %q; want %q", branch, yy00Unknown)
	}
}

// ---------------------------------------------------------------------------
// Arm 5 — the fail-open must NOT generalise to ancestors.
// ---------------------------------------------------------------------------

// A permission-denied or otherwise unreadable ANCESTOR is not evidence that the
// stack beneath it is a repository. Widening the fail-open to every walk level
// would turn every stack under one such directory into a badge reading
// "unknown (read failed)" — a worse regression than the missing badges this
// change fixes, and one that only appears in a deployment nobody tests on.
func TestResolveGitState_AncestorStatFaultDoesNotFailOpen(t *testing.T) {
	root := t.TempDir()
	ancestor := yy00MkdirAll(t, filepath.Join(root, "unreadable"))
	yy00LoopedGitEntry(t, ancestor)
	stack := yy00MkdirAll(t, filepath.Join(ancestor, "web"))

	isGitRepo, branch := resolveGitState(stack)

	if isGitRepo {
		t.Fatalf("isGitRepo = true because an ANCESTOR's .git could not be stat'd; want false. "+
			"Fail-open is scoped to the stack's own .git (branch = %q)", branch)
	}
	if branch != "" {
		t.Fatalf("branch = %q; want the empty string", branch)
	}
}

// ---------------------------------------------------------------------------
// Arm 6 — a RELATIVE gitdir pointer resolves against the ancestor holding the
// .git file, not against the directory the walk started from.
// ---------------------------------------------------------------------------

func TestResolveGitState_AncestorGitFileRelativePointerResolvesAgainstTheAncestor(t *testing.T) {
	root := t.TempDir()
	worktree := yy00MkdirAll(t, filepath.Join(root, "worktree"))
	realGitDir := yy00MkdirAll(t, filepath.Join(root, "realgit"))
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644); err != nil {
		t.Fatalf("seeding the pointed-to HEAD: %v", err)
	}
	// Relative, per gitrepository-layout(5), and relative to the directory
	// CONTAINING the .git file — which is `worktree`, not the stack below it.
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../realgit\n"), 0o644); err != nil {
		t.Fatalf("seeding the .git pointer file: %v", err)
	}
	stack := yy00MkdirAll(t, filepath.Join(worktree, "stacks", "web"))

	isGitRepo, branch := resolveGitState(stack)

	if !isGitRepo {
		t.Fatalf("isGitRepo = false under an ancestor whose .git is a pointer file; want true")
	}
	// The whole string. Resolving "../realgit" against the STACK path instead
	// of against the ancestor yields a path that does not exist, which surfaces
	// as the unknown branch rather than as a visible failure — so asserting
	// only isGitRepo would pass with the wrong base.
	if branch != "feature" {
		t.Fatalf("branch = %q; want %q. %q means the relative gitdir pointer was resolved against "+
			"the stack path instead of against the ancestor holding the .git file",
			branch, "feature", yy00Unknown)
	}
}

// ---------------------------------------------------------------------------
// Termination. resolveGitState takes a bare path string and nothing forces it
// absolute; filepath.Dir(".") is ".", so a walk written without filepath.Abs
// never terminates on a relative path. In production the path is always
// absolute, so this hangs only under a test — which is exactly why one exists.
//
// The assertion is the returned value, not the elapsed time: a hang fails as a
// package timeout, and the value arm still discriminates a wrong answer from a
// right one once it returns.
// ---------------------------------------------------------------------------

func TestResolveGitState_RelativePathTerminates(t *testing.T) {
	root := t.TempDir()
	plain := yy00MkdirAll(t, filepath.Join(root, "plain"))
	yy00RequireCleanAncestry(t, plain)
	t.Chdir(root)

	isGitRepo, branch := resolveGitState("plain")

	if isGitRepo || branch != "" {
		t.Fatalf("resolveGitState(%q) = (%v, %q); want (false, \"\")", "plain", isGitRepo, branch)
	}
}
