package services

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// hiddenSettingsDB returns a fully migrated ON-DISK database plus hide/restore
// closures that make its `settings` table transiently unreadable through a
// SECOND connection opened by the test.
//
// Why this shape and not faultyDB / closedDBWithSettings: a closed or
// hard-broken database SELF-PROTECTS against this defect. pruneStaleStacks
// reads scan_depth, builds activeDirs, and only then calls
// s.db.ListDirectories() (scanner.go:598), whose error check returns before any
// delete. The destructive window is a TRANSIENT read fault -- scan_depth fails
// while directories still answers -- which is what a writer holding the lock
// past busy_timeout(5000) (database.go:98) produces in production.
//
// Constraints, each of which is load-bearing:
//   - The dataDir MUST be a real directory, never ":memory:". NewWithEncryptor
//     pins SetMaxOpenConns(1) for the in-memory DSN (database.go:109) and
//     in-memory SQLite is per-connection, so a side connection would be a
//     DIFFERENT database.
//   - The fault arrives as "no such table: settings", NOT sql.ErrNoRows, which
//     is exactly the branch the fix must discriminate.
//   - A per-key NULL row is NOT an alternative: settings is
//     `key TEXT PRIMARY KEY, value TEXT NOT NULL` (migrations.go:165-168) and
//     the insert is rejected with NOT NULL constraint failed (proven by the
//     agent-os-l42o worker, not re-tested here).
func hiddenSettingsDB(t *testing.T) (*database.DB, func(), func()) {
	t.Helper()

	dataDir := t.TempDir()
	db, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault("", "obgr-scanner-dbfault-key-32-chars"))
	require.NoError(t, err, "open migrated on-disk db")
	t.Cleanup(func() { _ = db.Close() })

	side, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err, "open side connection")
	t.Cleanup(func() { _ = side.Close() })

	hidden := false
	hide := func() {
		t.Helper()
		_, err := side.Exec("ALTER TABLE settings RENAME TO settings_hidden")
		require.NoError(t, err, "hide settings table")
		hidden = true
	}
	restore := func() {
		if !hidden {
			return
		}
		_, err := side.Exec("ALTER TABLE settings_hidden RENAME TO settings")
		require.NoError(t, err, "restore settings table")
		hidden = false
	}
	t.Cleanup(restore)
	return db, hide, restore
}

// TestHiddenSettingsDB_FaultsOnlyTheSettingsRead is the INSTRUMENT'S OWN
// CONTROL. Every refusal assertion in this file is worthless if the fixture
// merely breaks the whole database, because a broken database is caught by the
// ListDirectories guard and never reaches a delete. This proves the fault is
// narrow (settings unreadable, directories healthy) and transient (the same
// read succeeds again after restore).
func TestHiddenSettingsDB_FaultsOnlyTheSettingsRead(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("scan_depth", "2"))
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/probe/dir", Name: "dir", RootDir: "/probe"}))

	hide()
	v, err := db.GetSetting("scan_depth")
	t.Logf("FAULTED   GetSetting      -> %q err=%v", v, err)
	require.Error(t, err, "settings must be unreadable while hidden")
	require.NotErrorIs(t, err, sql.ErrNoRows,
		"the fault must NOT be sql.ErrNoRows -- that is the arm the fix keeps, not the arm it refuses on")

	dirs, dirErr := db.ListDirectories()
	t.Logf("FAULTED   ListDirectories -> %d rows err=%v", len(dirs), dirErr)
	require.NoError(t, dirErr, "directories must stay readable, or the ListDirectories guard would mask the defect")
	require.Len(t, dirs, 1)

	restore()
	v, err = db.GetSetting("scan_depth")
	t.Logf("RECOVERED GetSetting      -> %q err=%v", v, err)
	require.NoError(t, err)
	require.Equal(t, "2", v)
}

// depth2Tree lays out <base>/stacks/<parent>/<child> and returns the root, the
// depth-1 parent and the depth-2 child. collectActiveDirs(root, 1, 1, active)
// marks the root and the parent only, so the child is what a depth-1 sweep
// deletes.
func depth2Tree(t *testing.T) (root, parent, child string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "stacks")
	parent = filepath.Join(root, "group")
	child = filepath.Join(parent, "deepstack")
	require.NoError(t, os.MkdirAll(child, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(child, "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0644))
	return root, parent, child
}

// obgrGitUser and obgrGitToken stand in for the payload that makes this a P1
// rather than a nuisance. migrations.go:151 declares
// `FOREIGN KEY (directory) REFERENCES directories(path) ON DELETE CASCADE` and
// foreign_keys(1) is set on every connection (database.go:98), so deleting a
// directory row takes its stacks with it. The stack rows are recoverable -- the
// next successful scan re-mints them under the SAME path-derived IDs -- but the
// DIRECTORY row is not: it carries git_auth_type, git_ssh_key_path,
// git_https_user and the ENCRYPTED git_https_token (migrations.go:255-258).
// A transient lock therefore permanently destroys the per-directory git
// credentials for every directory below depth 1.
//
// This is why every assertion below is on the DIRECTORY rows and their
// credentials, not only on the stack rows: a stack-only assertion passes
// against the live defect, because the stacks come back.
const (
	obgrGitUser  = "obgr-https-user"
	obgrGitToken = "obgr-https-token-not-recoverable-by-a-rescan"
)

// seedDepth2Rows persists the directory rows a prior successful depth-2 scan
// would have left behind, gives the DEPTH-2 child real git credentials, and
// attaches one stack row to the child.
func seedDepth2Rows(t *testing.T, db *database.DB, root, parent, child string) {
	t.Helper()
	for _, d := range []struct{ path, name string }{{parent, "group"}, {child, "deepstack"}} {
		require.NoError(t, db.UpsertDirectory(models.Directory{Path: d.path, Name: d.name, RootDir: root}))
	}
	require.NoError(t, db.UpdateDirectoryCredentials(child, "https", "", obgrGitUser, obgrGitToken))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stacks~group~deepstack:default", Directory: child,
		ComposeFile: "compose.yaml", ProjectName: "deepstack",
	}))
}

// requireDepth2RowsIntact asserts the whole payload survived: both directory
// rows, the child's git credentials, and the cascaded stack row.
func requireDepth2RowsIntact(t *testing.T, db *database.DB, parent, child string) {
	t.Helper()
	paths := directoryPaths(t, db)
	require.Contains(t, paths, parent, "the depth-1 directory row must survive")
	require.Contains(t, paths, child, "the depth-2 directory row must survive -- it is the one that is NOT recoverable by a rescan")

	creds, err := db.GetDirectoryCredentials(child)
	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, obgrGitUser, creds.GitHTTPSUser)
	require.Equal(t, obgrGitToken, creds.GitHTTPSToken, "the encrypted git token must still be readable")

	stacks, err := db.ListStacksByDirectory(child)
	require.NoError(t, err)
	require.Len(t, stacks, 1, "the stack row must not have been taken by the directory's ON DELETE CASCADE")
}

func directoryPaths(t *testing.T, db *database.DB) []string {
	t.Helper()
	dirs, err := db.ListDirectories()
	require.NoError(t, err)
	paths := make([]string, 0, len(dirs))
	for _, d := range dirs {
		paths = append(paths, d.Path)
	}
	return paths
}

// TestPruneStaleStacks_TransientScanDepthFault_DeletesNothing is the failing-first
// arm of agent-os-obgr.
//
// Before the fix, scanner.go:587 read scan_depth as
// `if depthStr, err := s.db.GetSetting("scan_depth"); err == nil && depthStr != ""`,
// so an unreadable database and an absent row were the same event: scanDepth
// silently fell back to 1, collectActiveDirs marked only the root and its
// immediate children, and the loops below deleted the directory row (and, by
// cascade, the stacks) of everything deeper. Data loss produced by a READ
// error, not by any write the operator asked for.
func TestPruneStaleStacks_TransientScanDepthFault_DeletesNothing(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	root, parent, child := depth2Tree(t)
	require.NoError(t, db.SetSetting("scan_depth", "2"))
	seedDepth2Rows(t, db, root, parent, child)

	service := NewScannerService(&config.Config{StacksDir: root, DataDir: t.TempDir()}, db)
	buf := captureSlog(t)

	hide()
	err := service.pruneStaleStacks()
	restore()

	// The ROW assertions come first deliberately: they are the data loss, and
	// they are what must be seen failing on the pre-fix code. Asserting the
	// returned error first would short-circuit the test before it ever looked
	// at the rows, and a red arm that only says "no error was returned" is not
	// evidence that anything was deleted.
	requireDepth2RowsIntact(t, db, parent, child)

	require.Error(t, err, "a scan depth that could not be read must abort the prune, not fall back to 1")

	out := buf.String()
	require.Contains(t, out, "level=ERROR", "the refusal must be an ERROR, not the caller's causeless WARN at scanner.go:502")
	require.Contains(t, out, "cause=", "the ERROR must carry the discriminated cause")
	require.Contains(t, out, "no such table: settings", "the cause must be the underlying driver error")

	// The fault really was transient: the same read answers again afterwards.
	v, getErr := db.GetSetting("scan_depth")
	require.NoError(t, getErr)
	require.Equal(t, "2", v)
}

// TestPruneStaleStacks_HealthyNoScanDepthRow_PrunesAtDepth1 is CONTROL 1: the
// fresh-install case. An ABSENT scan_depth row arrives as sql.ErrNoRows and
// must keep today's behaviour byte-for-byte -- depth 1, and the depth-2 row
// pruned -- with NO ERROR logged. Without this arm, "refuses on a fault" and
// "never prunes anything" are indistinguishable.
func TestPruneStaleStacks_HealthyNoScanDepthRow_PrunesAtDepth1(t *testing.T) {
	db, _, _ := hiddenSettingsDB(t)
	root, parent, child := depth2Tree(t)
	seedDepth2Rows(t, db, root, parent, child)

	service := NewScannerService(&config.Config{StacksDir: root, DataDir: t.TempDir()}, db)
	buf := captureSlog(t)

	require.NoError(t, service.pruneStaleStacks())

	require.NotContains(t, buf.String(), "level=ERROR",
		"an unset scan_depth is legitimate, not a fault -- it must not log an ERROR")

	paths := directoryPaths(t, db)
	require.Contains(t, paths, parent, "the depth-1 row is active at depth 1")
	require.NotContains(t, paths, child, "today's depth-1 behaviour, preserved byte-for-byte: the depth-2 row is pruned")
}

// TestPruneStaleStacks_HealthyScanDepth2_PrunesOnlyWhatIsGone is CONTROL 2:
// with a readable scan_depth=2 the depth-2 rows are kept AND a row whose
// directory is genuinely gone from disk is still deleted. The second half is
// the part that matters: it proves the fix narrowed the delete to the fault
// case rather than disabling pruning.
func TestPruneStaleStacks_HealthyScanDepth2_PrunesOnlyWhatIsGone(t *testing.T) {
	db, _, _ := hiddenSettingsDB(t)
	root, parent, child := depth2Tree(t)
	require.NoError(t, db.SetSetting("scan_depth", "2"))
	seedDepth2Rows(t, db, root, parent, child)

	vanished := filepath.Join(root, "deleted-on-disk")
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: vanished, Name: "deleted-on-disk", RootDir: root}))

	service := NewScannerService(&config.Config{StacksDir: root, DataDir: t.TempDir()}, db)
	buf := captureSlog(t)

	require.NoError(t, service.pruneStaleStacks())
	require.NotContains(t, buf.String(), "level=ERROR")

	requireDepth2RowsIntact(t, db, parent, child)
	require.NotContains(t, directoryPaths(t, db), vanished,
		"a directory that is really gone must still be pruned")
}

// TestScanAll_TransientScanDepthFault_RefusesAndLogsCause covers the sibling
// site at scanner.go:491, which read the same key with the same shape (receiver
// dbErr). There the immediate consequence was only a silently shallow scan --
// but ScanAll calls pruneStaleStacks at :502, so on a persistent fault both
// reads fault together and the shallow scan agrees with the deep delete.
//
// ScanAll REFUSES rather than only logging: handlers/directories.go:68 already
// maps its error to a 500 with the cause, and an operator-triggered rescan that
// silently ran one level deep and reported 200 OK is a wrong answer presented
// as success. cmd/server/main.go:311 discards the error, so boot only skips the
// scan; the scan path itself upserts and never deletes, so refusing costs
// nothing that the watcher's next pass does not recover.
func TestScanAll_TransientScanDepthFault_RefusesAndLogsCause(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	root, parent, child := depth2Tree(t)
	require.NoError(t, db.SetSetting("scan_depth", "2"))
	seedDepth2Rows(t, db, root, parent, child)

	service := NewScannerService(&config.Config{StacksDir: root, DataDir: t.TempDir()}, db)
	buf := captureSlog(t)

	hide()
	_, err := service.ScanAll()
	restore()

	// Rows first, for the same reason as the prune test above.
	requireDepth2RowsIntact(t, db, parent, child)

	require.Error(t, err, "ScanAll must not report success after silently falling back to depth 1")

	out := buf.String()
	require.Contains(t, out, "level=ERROR")
	require.Contains(t, out, "cause=")
	require.Contains(t, out, "no such table: settings")
}

// TestScanAll_HealthyNoScanDepthRow_Succeeds is the matching control for the
// site above: the fresh-install DB must still scan and return no error.
func TestScanAll_HealthyNoScanDepthRow_Succeeds(t *testing.T) {
	db, _, _ := hiddenSettingsDB(t)
	root, _, _ := depth2Tree(t)

	service := NewScannerService(&config.Config{StacksDir: root, DataDir: t.TempDir()}, db)
	buf := captureSlog(t)

	_, err := service.ScanAll()
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "level=ERROR")
}
