package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// agent-os-rzpw: a save through a stack's symlinked compose file writes the
// link TARGET (handlers' resolveWriteTarget renames a temp file into place in
// the target's folder), so the watcher saw an event only in the target's
// folder and rescanned that one: the linking stack kept its old ProjectName
// until a full scan. Producer: the write landing in the target's folder.
// Consumer: handleEvent, which rescanned only filepath.Dir(event.Name).

func TestWatcherRzpw_SaveThroughLinkedComposeUpdatesTheLinkingStack(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	s1 := filepath.Join(root, "s1")
	require.NoError(t, os.Mkdir(shared, 0o755))
	require.NoError(t, os.Mkdir(s1, 0o755))
	target := filepath.Join(shared, "compose.yaml")
	require.NoError(t, os.WriteFile(target, composeNamed("first"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join("..", "shared", "compose.yaml"), filepath.Join(s1, "compose.yaml")))
	w, s := newTestWatcher(t, root)
	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Equal(t, "first", projectNameIn(t, s, s1), "control: s1 is registered through the link")
	require.NoError(t, w.Start())

	// The editor's save, as writeFileAtomic does it: a temp file renamed over
	// the link's target.
	tmp := filepath.Join(shared, ".compose.yaml.tmp")
	require.NoError(t, os.WriteFile(tmp, composeNamed("second"), 0o644))
	require.NoError(t, os.Rename(tmp, target))

	require.Eventually(t, func() bool { return projectNameIn(t, s, shared) == "second" },
		10*time.Second, 20*time.Millisecond, "control: the target's own folder is rescanned")
	require.Eventually(t, func() bool { return projectNameIn(t, s, s1) == "second" },
		10*time.Second, 20*time.Millisecond, "the stack whose compose file links to the saved file must be rescanned")
}

// stacksLinkingTo names only the stacks whose compose file resolves to the
// changed file.
func TestWatcherRzpw_OnlyStacksLinkingToTheChangedFileAreRescanned(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	other := filepath.Join(root, "other")
	s1 := filepath.Join(root, "s1")
	for _, d := range []string{shared, other, s1} {
		require.NoError(t, os.Mkdir(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(shared, "compose.yaml"), composeNamed("first"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(other, "compose.yaml"), composeNamed("other"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join("..", "shared", "compose.yaml"), filepath.Join(s1, "compose.yaml")))
	w, s := newTestWatcher(t, root)
	_, err := s.ScanAll()
	require.NoError(t, err)

	require.Equal(t, []string{s1}, w.stacksLinkingTo(filepath.Join(shared, "compose.yaml")), "control: s1 links to shared/compose.yaml")
	require.Empty(t, w.stacksLinkingTo(filepath.Join(other, "compose.yaml")), "no stack links to other/compose.yaml")
}
