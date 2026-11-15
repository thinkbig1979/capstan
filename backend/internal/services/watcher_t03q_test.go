package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-t03q: the scanner registers stacks down to the configured scan
// depth, but the watcher watched only one level below each stacks dir, so a
// stack at depth 2 or deeper never triggered a rescan. Producer:
// scanDirectoryRecursive registering deeper stacks. Consumer: the watch set
// (addWatchPaths at boot, watchNewDir at runtime).

func watchListHas(w *WatcherService, path string) bool {
	for _, p := range w.watcher.WatchList() {
		if p == path {
			return true
		}
	}
	return false
}

func TestWatcherT03q_ComposeEditAtScanDepth2Rescans(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "team", "app")
	require.NoError(t, os.MkdirAll(app, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.yaml"), composeNamed("first"), 0o644))
	w, s := newTestWatcher(t, root)
	require.NoError(t, s.db.SetSetting("scan_depth", "2"))
	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Equal(t, "first", projectNameIn(t, s, app), "control: the scanner registers the depth-2 stack")
	require.NoError(t, w.Start())

	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.yaml"), composeNamed("second"), 0o644))
	require.Eventually(t, func() bool { return projectNameIn(t, s, app) == "second" },
		10*time.Second, 20*time.Millisecond,
		"a compose edit at scan depth 2 must rescan; watch list: %v", w.watcher.WatchList())
}

// A folder tree created at runtime (mkdir -p, a mv in, a clone) brings
// subfolders that fire no events of their own once their parent is watched.
func TestWatcherT03q_RuntimeTreeAtScanDepth2IsWatchedAndRegistered(t *testing.T) {
	root := t.TempDir()
	w, s := newTestWatcher(t, root)
	require.NoError(t, s.db.SetSetting("scan_depth", "2"))
	require.NoError(t, w.Start())

	staging := t.TempDir()
	built := filepath.Join(staging, "team")
	require.NoError(t, os.MkdirAll(filepath.Join(built, "app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(built, "app", "compose.yaml"), composeNamed("first"), 0o644))
	team := filepath.Join(root, "team")
	require.NoError(t, os.Rename(built, team))

	app := filepath.Join(team, "app")
	require.Eventually(t, func() bool { return projectNameIn(t, s, app) == "first" },
		10*time.Second, 20*time.Millisecond, "a stack moved in at depth 2 must be registered")
	require.Eventually(t, func() bool { return watchListHas(w, app) },
		10*time.Second, 20*time.Millisecond, "the depth-2 folder must be watched; watch list: %v", w.watcher.WatchList())

	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.yaml"), composeNamed("second"), 0o644))
	require.Eventually(t, func() bool { return projectNameIn(t, s, app) == "second" },
		10*time.Second, 20*time.Millisecond, "an edit in the moved-in depth-2 folder must rescan")
}

// The watcher stops at the scan depth and skips dot folders, as the scanner
// does, both at boot and at runtime.
func TestWatcherT03q_WatchStopsAtScanDepthAndSkipsDotFolders(t *testing.T) {
	root := t.TempDir()
	bootDeep := filepath.Join(root, "team", "app", "data")
	bootHidden := filepath.Join(root, "team", ".cache")
	require.NoError(t, os.MkdirAll(bootDeep, 0o755))
	require.NoError(t, os.MkdirAll(bootHidden, 0o755))
	w, s := newTestWatcher(t, root)
	require.NoError(t, s.db.SetSetting("scan_depth", "2"))
	require.NoError(t, w.Start())

	assert.True(t, watchListHas(w, filepath.Join(root, "team", "app")), "control: depth 2 is watched at boot")
	assert.False(t, watchListHas(w, bootDeep), "depth 3 is beyond scan depth 2")
	assert.False(t, watchListHas(w, bootHidden), "a dot folder is not watched")

	runDeep := filepath.Join(root, "team", "app", "logs")
	runHidden := filepath.Join(root, "team", ".tmp")
	sentinel := filepath.Join(root, "team", "sentinel")
	require.NoError(t, os.Mkdir(runDeep, 0o755))
	require.NoError(t, os.Mkdir(runHidden, 0o755))
	require.NoError(t, os.Mkdir(sentinel, 0o755)) // created last: once watched, the earlier events were handled
	require.Eventually(t, func() bool { return watchListHas(w, sentinel) },
		10*time.Second, 20*time.Millisecond, "control: a runtime depth-2 folder is watched")

	watched := w.watcher.WatchList()
	assert.NotContains(t, watched, runDeep, "a runtime depth-3 folder is beyond scan depth 2")
	assert.NotContains(t, watched, runHidden, "a runtime dot folder is not watched")
}
