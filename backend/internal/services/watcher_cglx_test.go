package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
