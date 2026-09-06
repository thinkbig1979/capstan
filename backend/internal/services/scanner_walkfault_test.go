package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-6wbu. collectActiveDirs answered an os.ReadDir fault with a bare
// `return`, so "this directory has no subdirectories" and "I could not find out
// what is under this directory" produced the same map. pruneStaleStacks then
// DELETES the directories row of everything absent from that map, and
// migrations.go:151 declares ON DELETE CASCADE on stacks(directory), so a single
// transient read fault destroys live stack rows and the git credentials stored
// on them. Same harm agent-os-obgr fixed one function away in this file, for the
// other input (scan depth) to the same map.
//
// THE FIXTURE IS NOT chmod. `chmod 000` is a no-op for root (CAP_DAC_OVERRIDE),
// which is why integrationtest/compose_env_test.go:571-573 has to skip itself as
// root — and that file is behind the `integration` build tag, i.e. out of gate.
// ENOTDIR is a structural property of the path, resolved before any permission
// check, so no uid and no capability can defeat it. MEASURED on this box for
// os.ReadDir, both arms on one instrument:
//
//	ENOTDIR  (regular file as the path)  err=readdirent …: not a directory     IsNotExist=false
//	ENOENT   (path never created)        err=open …: no such file or directory IsNotExist=true
//
// WHY THE NON-ENOENT ARM IS TESTED AT THE ROOT AND NOT AT A SUBDIRECTORY: the
// bead asked for a subdirectory whose ReadDir returns a non-ENOENT error, and
// that tree cannot be built. collectActiveDirs only recurses into entries whose
// DirEntry.IsDir() is true, and IsDir() is dirent-type-based — a regular file
// (ENOTDIR) and a symlink (ELOOP) both report false and are skipped before any
// recursion, and a path component over 255 bytes (ENAMETOOLONG) cannot be
// created in the first place. All three faults ARE reachable at currentDepth==1,
// where the path comes straight out of config.GetAllStacksDirs() and nothing has
// filtered it — which is also where the blast radius is largest.
//
// Every test here asserts THE ROWS, and asserts them BEFORE the error: an
// arm that led with require.Error would short-circuit and never print the
// deletion the defect is actually about.

// wbuSeedStack records a directories row and one stack row under it, the state a
// previous healthy scan would have left behind. It writes nothing to disk: every
// test here controls disk state itself, and the whole point is that the rows and
// the filesystem can disagree.
func wbuSeedStack(t *testing.T, db *database.DB, root, dirPath, stackID string) {
	t.Helper()
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path:    dirPath,
		Name:    filepath.Base(dirPath),
		RootDir: root,
	}))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          stackID,
		Directory:   dirPath,
		ComposeFile: "compose.yaml",
		ProjectName: filepath.Base(dirPath),
	}))
}

func wbuNewDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// wbuRowsFor returns the directory paths and stack IDs currently in the DB.
func wbuRowsFor(t *testing.T, db *database.DB) ([]string, []string) {
	t.Helper()
	dirs, err := db.ListDirectories()
	require.NoError(t, err)
	dirPaths := make([]string, 0, len(dirs))
	for _, d := range dirs {
		dirPaths = append(dirPaths, d.Path)
	}
	stacks, err := db.ListStacks()
	require.NoError(t, err)
	stackIDs := make([]string, 0, len(stacks))
	for _, s := range stacks {
		stackIDs = append(stackIDs, s.ID)
	}
	return dirPaths, stackIDs
}

// RED ARM A — a non-ENOENT fault on a configured root. The root is a regular
// file, so os.ReadDir returns ENOTDIR: the directory listing could not be
// obtained, which says nothing at all about whether the stacks under it still
// exist. Pruning against that empty listing deletes every row beneath the root.
func TestPruneStaleStacks_RootReadDirENOTDIR_KeepsRows(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "stacks")
	require.NoError(t, os.WriteFile(root, []byte("a regular file where a directory is configured"), 0644))

	// Confirms the fixture arms, and that it arms as the NON-absent kind of
	// fault — the discrimination under test turns on exactly this.
	_, readErr := os.ReadDir(root)
	require.Error(t, readErr)
	require.False(t, os.IsNotExist(readErr), "fixture must produce a non-ENOENT fault, got %v", readErr)

	db := wbuNewDB(t)
	stackDir := filepath.Join(root, "mystack")
	wbuSeedStack(t, db, root, stackDir, "stacks~mystack:default")

	service := NewScannerService(&config.Config{StacksDir: root}, db)
	pruneErr := service.pruneStaleStacks()

	dirPaths, stackIDs := wbuRowsFor(t, db)
	assert.Contains(t, dirPaths, stackDir,
		"a root whose listing could not be read must not delete the directory rows beneath it")
	assert.Contains(t, stackIDs, "stacks~mystack:default",
		"the stack row must survive: DeleteDirectory cascades, so losing the directory row loses this and its git credentials")

	require.Error(t, pruneErr, "an unreadable configured root must be refused, not treated as empty")
}

// RED ARM B — ENOENT on a configured root: the unmounted-volume shape. This is
// the arm a flat "ENOENT means gone, prune it" rule ships live, and it is the
// maximal instance of the harm: nothing enumerated this path, so an unmounted
// volume, a mistyped stacks_dir and a genuinely emptied root are one event, and
// two of the three destroy every row under the root.
func TestPruneStaleStacks_RootMissingENOENT_KeepsRows(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "mnt", "stacks") // never created: the volume is not mounted

	_, readErr := os.ReadDir(root)
	require.Error(t, readErr)
	require.True(t, os.IsNotExist(readErr), "fixture must produce ENOENT, got %v", readErr)

	db := wbuNewDB(t)
	stackDir := filepath.Join(root, "mystack")
	wbuSeedStack(t, db, root, stackDir, "stacks~mystack:default")

	service := NewScannerService(&config.Config{StacksDir: root}, db)
	pruneErr := service.pruneStaleStacks()

	dirPaths, stackIDs := wbuRowsFor(t, db)
	assert.Contains(t, dirPaths, stackDir,
		"a configured root that is absent is an unmounted volume until proven otherwise; its subtree's rows must survive")
	assert.Contains(t, stackIDs, "stacks~mystack:default",
		"the stack row must survive the missing root")

	require.Error(t, pruneErr, "an absent configured root must be refused, not treated as an empty root")
}

// RED ARM C — partial failure across several configured roots must refuse
// WHOLLY. allDirs feeds ONE shared activeDirs map, so skipping the faulted root
// and carrying on would prune the HEALTHY root's rows against a map missing the
// faulted root's subtree. That is the same defect with an extra step, and it is
// the failure a per-root `continue` would introduce while looking like a fix.
func TestPruneStaleStacks_OneFaultedRootAmongSeveral_KeepsEveryRoot(t *testing.T) {
	base := t.TempDir()
	healthy := filepath.Join(base, "healthy")
	liveDir := filepath.Join(healthy, "live")
	require.NoError(t, os.MkdirAll(liveDir, 0755))
	faulted := filepath.Join(base, "faulted") // absent

	db := wbuNewDB(t)
	// A row under the healthy root that is genuinely stale: it is absent from
	// disk, so a completed walk WOULD delete it. It must survive anyway,
	// because the walk did not complete.
	staleDir := filepath.Join(healthy, "gone")
	wbuSeedStack(t, db, healthy, staleDir, "healthy~gone:default")
	wbuSeedStack(t, db, faulted, filepath.Join(faulted, "mystack"), "faulted~mystack:default")

	service := NewScannerService(&config.Config{
		StacksDir:       healthy,
		ExtraStacksDirs: []string{faulted},
	}, db)
	pruneErr := service.pruneStaleStacks()

	dirPaths, stackIDs := wbuRowsFor(t, db)
	assert.Contains(t, dirPaths, staleDir,
		"a fault on ANY configured root suspends pruning for all of them: the shared activeDirs map is incomplete, so no deletion decision made from it is sound")
	assert.Contains(t, stackIDs, "healthy~gone:default")
	assert.Contains(t, stackIDs, "faulted~mystack:default")

	require.Error(t, pruneErr)
}

// CONTROL 1 — and it is the half that stops the fix becoming a refusal to ever
// prune. Under a healthy root, a directory that is genuinely gone is still
// pruned exactly as before, rows and cascade included, while the live one is
// untouched. Both sides on one instrument, in one run.
//
// This is also the shape a removed directory ACTUALLY takes in production: the
// parent's ReadDir simply does not list it, so collectActiveDirs is never called
// on it and its own ReadDir never runs. Reaching the ENOENT branch below the
// root requires the directory to vanish inside the window between the parent's
// ReadDir and the recursive call; that race is covered directly by
// TestCollectActiveDirs_ENOENTPolarityByDepth below.
func TestPruneStaleStacks_HealthyRoot_PrunesGoneKeepsLive(t *testing.T) {
	root := t.TempDir()
	liveDir := filepath.Join(root, "live")
	require.NoError(t, os.MkdirAll(liveDir, 0755))
	goneDir := filepath.Join(root, "gone") // recorded in the DB, absent from disk

	db := wbuNewDB(t)
	wbuSeedStack(t, db, root, liveDir, "root~live:default")
	wbuSeedStack(t, db, root, goneDir, "root~gone:default")

	service := NewScannerService(&config.Config{StacksDir: root}, db)
	pruneErr := service.pruneStaleStacks()

	dirPaths, stackIDs := wbuRowsFor(t, db)
	assert.NotContains(t, dirPaths, goneDir,
		"a directory the healthy walk did not find is genuinely removed and must still be pruned")
	assert.NotContains(t, stackIDs, "root~gone:default",
		"the removed directory's stack must still go by cascade")
	assert.Contains(t, dirPaths, liveDir, "the live directory row must survive an ordinary prune")
	assert.Contains(t, stackIDs, "root~live:default", "the live stack row must survive an ordinary prune")

	require.NoError(t, pruneErr, "a healthy tree must prune without error, exactly as before")
}

// The depth polarity itself, on ONE instrument: the SAME absent path, walked as
// a configured root and as a child. A child path was listed by its parent's
// ReadDir moments earlier, so its disappearance is a genuine removal and the
// walk may continue; a root was enumerated by nobody, so its absence is
// indistinguishable from an unmounted volume and must refuse. add3405
// (agent-os-d5ff) recorded exactly this ambiguity as a scope limit it did not
// close, for the DataDir; depth is what closes it here.
//
// Called directly rather than through pruneStaleStacks because the child arm is
// otherwise a race window (see the CONTROL 1 comment) and cannot be staged.
func TestCollectActiveDirs_ENOENTPolarityByDepth(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "vanished")

	active := map[string]bool{}
	require.NoError(t, collectActiveDirs(absent, 3, 2, active),
		"a child that vanished between its parent's ReadDir and this call is genuinely removed: prune, as before")

	err := collectActiveDirs(absent, 3, 1, active)
	require.Error(t, err,
		"the same absent path as a CONFIGURED ROOT is an unmounted volume until proven otherwise: refuse")

	// A non-ENOENT fault refuses at EVERY depth, child included: EIO, ENOTDIR
	// and friends mean "could not find out", never "absent", at any depth.
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0644))
	require.Error(t, collectActiveDirs(notADir, 3, 2, active),
		"a non-ENOENT fault is not an absence at any depth")
}

// wbuOverlongChildTree builds a tree whose ROOT is readable but whose one
// CHILD directory cannot be read: the root path is grown to just under Linux's
// PATH_MAX (4096), so root + "/" + a 200-byte child name exceeds it and
// os.ReadDir on the child returns ENAMETOOLONG. That is the only deterministic
// way found to fault a ReadDir BELOW the configured root: ENOTDIR and ELOOP are
// both filtered out by the DirEntry.IsDir() check before any recursion happens,
// and a genuinely-removed child is never listed by its parent at all.
//
// The last mkdir goes through os.Root, whose openat-based resolution never
// passes the over-long full path to a syscall; every other component is created
// with an ordinary absolute MkdirAll because each of those paths is still short
// enough. No chdir is involved, so this is safe alongside the package's
// parallel tests.
//
// The helper asserts both arms itself rather than stating measured byte counts
// in prose, because the exact lengths depend on TMPDIR and would go stale:
// ReadDir(root) must succeed and list the child with IsDir()==true, and
// ReadDir(child) must fail with a non-ENOENT error. MEASURED on this box
// (uid=1000, TMPDIR=/tmp) while developing: root len 4037, err=<nil>; child len
// 4238, err="open …: file name too long", IsNotExist=false.
func wbuOverlongChildTree(t *testing.T) (root string) {
	t.Helper()
	seg := strings.Repeat("d", 200)
	root = t.TempDir()
	for len(root)+1+len(seg) <= 4096 {
		root = filepath.Join(root, seg)
	}
	require.NoError(t, os.MkdirAll(root, 0755))

	name := strings.Repeat("c", 200)
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	require.NoError(t, r.Mkdir(name, 0755))
	require.NoError(t, r.Close())

	child := filepath.Join(root, name)
	entries, err := os.ReadDir(root)
	require.NoError(t, err, "the ROOT must stay readable: the fault under test is the CHILD's")
	require.Len(t, entries, 1)
	require.True(t, entries[0].IsDir(), "the child must survive the IsDir filter, or the recursion is never entered")
	_, childErr := os.ReadDir(child)
	require.Error(t, childErr)
	require.False(t, os.IsNotExist(childErr), "the child fault must be non-ENOENT, got %v", childErr)
	return root
}

// The recursion has to PROPAGATE the refusal, not just produce it. A fault two
// levels down under-populates activeDirs exactly as a fault at the root does,
// so a recursive call whose error is dropped is the same defect one level in.
// Both halves asserted here: the walk reports the error, and the rows survive
// the prune that error is supposed to stop.
func TestPruneStaleStacks_ChildReadDirFault_PropagatesAndKeepsRows(t *testing.T) {
	root := wbuOverlongChildTree(t)

	db := wbuNewDB(t)
	require.NoError(t, db.SetSetting("scan_depth", "2")) // depth 1 never recurses, so the child fault would be unreachable
	stackDir := filepath.Join(root, "mystack")           // recorded, absent from disk: a completed walk WOULD prune it
	wbuSeedStack(t, db, root, stackDir, "root~mystack:default")

	active := map[string]bool{}
	require.Error(t, collectActiveDirs(root, 2, 1, active),
		"a fault on a child must reach the caller, not be dropped by the recursive call")
	require.True(t, active[root], "the walk still records what it did manage to see")

	service := NewScannerService(&config.Config{StacksDir: root, DataDir: t.TempDir()}, db)
	pruneErr := service.pruneStaleStacks()

	dirPaths, stackIDs := wbuRowsFor(t, db)
	assert.Contains(t, dirPaths, stackDir,
		"the walk did not complete, so nothing missing from activeDirs has been shown to be gone")
	assert.Contains(t, stackIDs, "root~mystack:default")

	require.Error(t, pruneErr)
}
