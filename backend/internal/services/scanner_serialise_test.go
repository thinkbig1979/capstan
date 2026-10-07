package services

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-z91e.6: pruneStaleStacks deletes every row whose path is missing
// from a disk walk taken BEFORE it reads the DB. A directory registered in
// that window (StacksHandler.Create, or the watcher) is not in the walk but is
// in ListDirectories, so an unserialised ScanAll deleted its directory row and
// its stack rows with it.

func newSerialiseTestScanner(t *testing.T) (*ScannerService, *database.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewScannerService(&config.Config{StacksDir: root}, db), db, root
}

// writeComposeStackDir returns its error rather than calling require, because
// the concurrent test calls it off the test goroutine.
func writeComposeStackDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644)
}

// registerAndScan is what StacksHandler.Create does once the directory and
// its compose file exist on disk.
func registerAndScan(s *ScannerService, dir, root string) error {
	if _, err := s.RegisterDirectory(dir, root); err != nil {
		return err
	}
	return s.ScanDirectoryWithRoot(dir, root)
}

// Operator-set git credentials for the directory created mid-scan. A rescan
// cannot restore them: UpsertDirectory's ON CONFLICT leaves the credential
// columns alone, so they are lost only when the prune deletes the row.
// SSH rather than HTTPS because the test DB has no encryption key, so it
// refuses to store an HTTPS token.
const (
	midScanAuthType   = "ssh"
	midScanSSHKeyPath = "/data/keys/operator-key"
)

// createLikeHandler mirrors StacksHandler.Create once the directory and its
// compose file exist on disk: RegisterDirectory, the handler's own
// UpsertStack, then the scan. Between the two scanner calls it also stores
// the git credentials an operator would set on the directory, which is the
// state a rescan cannot rebuild and the prune's cascade destroys. With
// RegisterDirectory alone unlocked, the final ScanDirectoryWithRoot would
// re-create the directory and stack rows after the prune, so asserting only
// that rows exist cannot see the loss (orch-dm-15, PR #546).
func createLikeHandler(s *ScannerService, db *database.DB, dir, root string) error {
	if _, err := s.RegisterDirectory(dir, root); err != nil {
		return err
	}
	stack := models.Stack{
		ID:          s.expectedStackID(dir, root, "compose.yaml"),
		Directory:   dir,
		ComposeFile: "compose.yaml",
		ProjectName: filepath.Base(dir),
		Status:      "stopped",
	}
	if err := db.UpsertStack(stack); err != nil {
		return err
	}
	if err := db.UpdateDirectoryCredentials(dir, midScanAuthType, midScanSSHKeyPath, "", ""); err != nil {
		return err
	}
	return s.ScanDirectoryWithRoot(dir, root)
}

func waitOrFail(t *testing.T, ch <-chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		require.NoError(t, err, what)
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out after 10s waiting for %s: a deadlock between the scanner's entry points", what)
	}
}

// parkScanAll starts ScanAll and returns once its prune has walked the disk
// and is parked before the DB reads. Callers must not use t.Parallel:
// pruneAfterWalkHook is a package variable. It is set before the ScanAll
// goroutine starts and cleared after the test, so the production read never
// races the test's writes.
func parkScanAll(t *testing.T, s *ScannerService) (release func(), scanDone <-chan error) {
	t.Helper()
	walked := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	pruneAfterWalkHook = func() {
		once.Do(func() {
			close(walked)
			<-released
		})
	}
	t.Cleanup(func() { pruneAfterWalkHook = nil })

	done := make(chan error, 1)
	go func() {
		_, err := s.ScanAll()
		done <- err
	}()

	select {
	case <-walked:
	case <-time.After(10 * time.Second):
		t.Fatal("ScanAll never reached the prune")
	}
	return func() { close(released) }, done
}

// runWhileParked runs op while ScanAll is parked, then releases the prune and
// waits for both. Unserialised, op completes while the prune is parked and the
// prune then deletes what it wrote. Serialised, op waits on the scanner lock
// and the 1s wait times out. The wait only decides how long the prune stays
// parked; on the fix the caller's assertions hold either way.
func runWhileParked(t *testing.T, release func(), scanDone <-chan error, what string, op func() error) {
	t.Helper()
	opDone := make(chan error, 1)
	go func() { opDone <- op() }()

	finishedWhileParked := false
	select {
	case err := <-opDone:
		require.NoError(t, err, what)
		finishedWhileParked = true
	case <-time.After(time.Second):
	}
	release()

	waitOrFail(t, scanDone, "ScanAll")
	if !finishedWhileParked {
		waitOrFail(t, opDone, what)
	}
}

// StacksHandler.Create's path.
func TestScannerService_ScanAll_PruneKeepsDirectoryRegisteredMidScan(t *testing.T) {
	s, db, root := newSerialiseTestScanner(t)
	release, scanDone := parkScanAll(t, s)

	// The prune's disk walk is over, so this directory is not in it.
	newDir := filepath.Join(root, "created-mid-scan")
	require.NoError(t, writeComposeStackDir(newDir))

	runWhileParked(t, release, scanDone, "the Create-shaped registration", func() error {
		return createLikeHandler(s, db, newDir, root)
	})

	creds, err := db.GetDirectoryCredentials(newDir)
	require.NoError(t, err, "the directory registered while ScanAll ran must survive its prune")
	assert.Equal(t, midScanAuthType, creds.GitAuthType, "git credentials stored while ScanAll ran must survive its prune")
	assert.Equal(t, midScanSSHKeyPath, creds.GitSSHKeyPath, "git credentials stored while ScanAll ran must survive its prune")

	stacks, err := db.ListStacksByDirectory(newDir)
	require.NoError(t, err)
	assert.Len(t, stacks, 1, "the stack created while ScanAll ran must survive its prune")
}

// The watcher's path: ScanDirectory with no RegisterDirectory first. Nothing
// re-scans the directory after the prune, so a row the prune deleted stays
// gone until the next full scan or file change.
func TestScannerService_ScanAll_PruneKeepsDirectoryRescannedMidScan(t *testing.T) {
	s, db, root := newSerialiseTestScanner(t)
	release, scanDone := parkScanAll(t, s)

	newDir := filepath.Join(root, "rescanned-mid-scan")
	require.NoError(t, writeComposeStackDir(newDir))

	runWhileParked(t, release, scanDone, "the watcher-shaped rescan", func() error {
		return s.ScanDirectory(newDir)
	})

	_, err := db.GetDirectory(newDir)
	require.NoError(t, err, "the directory rescanned while ScanAll ran must survive its prune")
	stacks, err := db.ListStacksByDirectory(newDir)
	require.NoError(t, err)
	assert.Len(t, stacks, 1, "the stack rescanned while ScanAll ran must survive its prune")
}

// Run with -race. Full scans and registrations of new directories interleave
// 50 times; every registered directory and its stack must still exist.
func TestScannerService_ConcurrentScanAllAndRegister_KeepsNewStacks(t *testing.T) {
	s, db, root := newSerialiseTestScanner(t)
	require.NoError(t, writeComposeStackDir(filepath.Join(root, "existing")))

	const iterations = 50
	scanErrs := make(chan error, 1)
	regErrs := make(chan error, 1)

	go func() {
		for i := 0; i < iterations; i++ {
			if _, err := s.ScanAll(); err != nil {
				scanErrs <- err
				return
			}
		}
		scanErrs <- nil
	}()
	go func() {
		for i := 0; i < iterations; i++ {
			dir := filepath.Join(root, fmt.Sprintf("new-%02d", i))
			if err := writeComposeStackDir(dir); err != nil {
				regErrs <- err
				return
			}
			if err := registerAndScan(s, dir, root); err != nil {
				regErrs <- err
				return
			}
		}
		regErrs <- nil
	}()

	waitOrFail(t, scanErrs, "the ScanAll loop")
	waitOrFail(t, regErrs, "the registration loop")

	for i := 0; i < iterations; i++ {
		dir := filepath.Join(root, fmt.Sprintf("new-%02d", i))
		_, err := db.GetDirectory(dir)
		require.NoError(t, err, "directory row for %s", dir)
		stacks, err := db.ListStacksByDirectory(dir)
		require.NoError(t, err)
		assert.Len(t, stacks, 1, "stack rows for %s", dir)
	}
}
