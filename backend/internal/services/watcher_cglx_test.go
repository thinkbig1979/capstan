package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-cglx: a stack folder renamed inside a stacks dir was watched only
// some of the time. inotify hands Add(new) the old folder's watch descriptor
// (same inode), fsnotify reuses the old entry, and its IN_MOVE_SELF cleanup
// then removes that shared kernel watch. Producer: fsnotify's Rename/Create
// pair and its IN_MOVE_SELF cleanup. Consumer: handleEvent -> watchNewDir.

func projectNameIn(t *testing.T, s *ScannerService, dir string) string {
	t.Helper()
	stacks, err := s.db.ListStacksByDirectory(dir)
	require.NoError(t, err)
	if len(stacks) == 0 {
		return ""
	}
	return stacks[0].ProjectName
}

func composeNamed(name string) []byte {
	return []byte("name: " + name + "\nservices:\n  web:\n    image: nginx\n")
}

func TestWatcherCglx_RenamedStackFolderStaysWatched(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	require.NoError(t, os.Mkdir(a, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(a, "compose.yaml"), composeNamed("first"), 0o644))
	w, s := newTestWatcher(t, root)
	require.NoError(t, w.Start())

	b := filepath.Join(root, "b")
	require.NoError(t, os.Rename(a, b))
	// Wait for the rescan watchNewDir schedules, so the edit below can only be
	// seen through a live watch on b, not picked up by that pending rescan.
	require.Eventually(t, func() bool { return projectNameIn(t, s, b) == "first" },
		10*time.Second, 20*time.Millisecond, "control: the renamed folder is registered")

	require.NoError(t, os.WriteFile(filepath.Join(b, "compose.yaml"), composeNamed("second"), 0o644))
	require.Eventually(t, func() bool { return projectNameIn(t, s, b) == "second" },
		10*time.Second, 20*time.Millisecond,
		"an edit inside the renamed folder must rescan it; watch list: %v", w.watcher.WatchList())
}

// The deterministic arm: the real-fsnotify test above goes red only when the
// race lands, so a revert of unwatchTree would pass it most of the time. This
// drives handleEvent with the Rename fsnotify delivers for the old name and
// requires the old folder and the watched folder below it to be unwatched.
func TestWatcherCglx_RenameEventUnwatchesTheOldFolderAndItsSubfolders(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	sub := filepath.Join(a, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	w, s := newTestWatcher(t, root)
	require.NoError(t, s.db.SetSetting("scan_depth", "2"))
	require.NoError(t, w.Start())
	require.True(t, watchListHas(w, a), "control: a is watched")
	require.True(t, watchListHas(w, sub), "control: a/sub is watched")

	w.handleEvent(fsnotify.Event{Name: a, Op: fsnotify.Rename})

	watched := w.watcher.WatchList()
	assert.NotContains(t, watched, a, "the renamed-away folder must be unwatched")
	assert.NotContains(t, watched, sub, "a watched folder below it must be unwatched too")
	assert.Contains(t, watched, root, "control: the stacks dir stays watched")
}
