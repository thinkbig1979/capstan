package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-3uu8: handleEvent ran the filename filter before its new-directory
// branch, so a stack folder created while Capstan runs (any name that is not a
// compose or .env name) was never watched and edits inside it triggered no
// rescan. Producer: fsnotify Create for a directory. Consumer: handleEvent.

const composeBody = "services:\n  web:\n    image: nginx\n"

func stackCount(t *testing.T, s *ScannerService) int {
	t.Helper()
	stacks, err := s.db.ListStacks()
	require.NoError(t, err)
	return len(stacks)
}

func TestWatcher3uu8_RuntimeCreatedStackDirIsWatchedAndRescanned(t *testing.T) {
	root := t.TempDir()
	w, scanner := newTestWatcher(t, root)
	require.NoError(t, w.Start())

	newStack := filepath.Join(root, "newstack")
	require.NoError(t, os.Mkdir(newStack, 0o755))
	require.Eventually(t, func() bool {
		for _, p := range w.watcher.WatchList() {
			if p == newStack {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "the new folder must be added to the watcher")

	require.NoError(t, os.WriteFile(filepath.Join(newStack, "compose.yaml"), []byte(composeBody), 0o644))

	require.Eventually(t, func() bool { return stackCount(t, scanner) == 1 },
		5*time.Second, 20*time.Millisecond, "a compose file written after the folder appeared must be rescanned")
}

// A compose file written before the watcher has added the folder fires no
// event the watcher can see, so adding the folder must also rescan it.
func TestWatcher3uu8_ComposeWrittenBeforeTheFolderIsWatchedIsStillRegistered(t *testing.T) {
	root := t.TempDir()
	w, scanner := newTestWatcher(t, root)
	require.NoError(t, w.Start())

	newStack := filepath.Join(root, "raced")
	require.NoError(t, os.Mkdir(newStack, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(newStack, "compose.yaml"), []byte(composeBody), 0o644))

	require.Eventually(t, func() bool { return stackCount(t, scanner) == 1 },
		5*time.Second, 20*time.Millisecond)
}

// The boot walk watches the stacks dir and its direct subfolders only, so a
// runtime Add must stop at the same depth, and skip dot folders.
func TestWatcher3uu8_RuntimeAddsMatchTheBootDepthRule(t *testing.T) {
	root := t.TempDir()
	stack := filepath.Join(root, "web")
	require.NoError(t, os.Mkdir(stack, 0o755))
	w, _ := newTestWatcher(t, root)
	require.NoError(t, w.Start())

	deeper := filepath.Join(stack, "data")
	hidden := filepath.Join(root, ".cache")
	sentinel := filepath.Join(root, "sentinel")
	require.NoError(t, os.Mkdir(deeper, 0o755))
	require.NoError(t, os.Mkdir(hidden, 0o755))
	require.NoError(t, os.Mkdir(sentinel, 0o755)) // created last: once watched, the earlier events were handled

	require.Eventually(t, func() bool {
		for _, p := range w.watcher.WatchList() {
			if p == sentinel {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "control: the depth-1 folder is watched")

	watched := w.watcher.WatchList()
	assert.NotContains(t, watched, deeper, "a folder two levels below the stacks dir is not watched at boot, so not at runtime")
	assert.NotContains(t, watched, hidden, "a dot folder is not watched at boot, so not at runtime")
}

// Configured dirs can carry a trailing slash (settings, env); the membership
// check compares cleaned paths.
func TestWatcher3uu8_StacksDirWithTrailingSlashStillMatches(t *testing.T) {
	root := t.TempDir()
	w, _ := newTestWatcher(t, root+string(os.PathSeparator))
	require.NoError(t, w.Start())

	newStack := filepath.Join(root, "slashed")
	require.NoError(t, os.Mkdir(newStack, 0o755))

	require.Eventually(t, func() bool {
		for _, p := range w.watcher.WatchList() {
			if p == newStack {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)
}
