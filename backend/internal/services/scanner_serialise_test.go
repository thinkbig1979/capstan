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

func waitOrFail(t *testing.T, ch <-chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		require.NoError(t, err, what)
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out after 10s waiting for %s: a deadlock between the scanner's entry points", what)
	}
}

// No t.Parallel: pruneAfterWalkHook is a package variable.
func TestScannerService_ScanAll_PruneKeepsDirectoryRegisteredMidScan(t *testing.T) {
	s, db, root := newSerialiseTestScanner(t)

	walked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// Set before the ScanAll goroutine starts and cleared after it has
	// returned, so the production read never races the test's writes.
	pruneAfterWalkHook = func() {
		once.Do(func() {
			close(walked)
			<-release
		})
	}
	t.Cleanup(func() { pruneAfterWalkHook = nil })

	scanDone := make(chan error, 1)
	go func() {
		_, err := s.ScanAll()
		scanDone <- err
	}()

	select {
	case <-walked:
	case <-time.After(10 * time.Second):
		t.Fatal("ScanAll never reached the prune")
	}

	// The prune's disk walk is over, so this directory is not in it.
	newDir := filepath.Join(root, "created-mid-scan")
	require.NoError(t, writeComposeStackDir(newDir))

	registerDone := make(chan error, 1)
	go func() { registerDone <- registerAndScan(s, newDir, root) }()

	// Unserialised, the registration completes while the prune is parked and
	// the prune then deletes it. Serialised, it waits on the scanner lock and
	// this times out. The wait only decides how long the prune stays parked;
	// the assertions below hold either way on the fix.
	registeredWhileParked := false
	select {
	case err := <-registerDone:
		require.NoError(t, err)
		registeredWhileParked = true
	case <-time.After(time.Second):
	}
	close(release)

	waitOrFail(t, scanDone, "ScanAll")
	if !registeredWhileParked {
		waitOrFail(t, registerDone, "RegisterDirectory + ScanDirectoryWithRoot")
	}

	dir, err := db.GetDirectory(newDir)
	require.NoError(t, err, "the directory registered while ScanAll ran must survive its prune")
	assert.Equal(t, newDir, dir.Path)

	stacks, err := db.ListStacksByDirectory(newDir)
	require.NoError(t, err)
	assert.Len(t, stacks, 1, "the stack created while ScanAll ran must survive its prune")
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
