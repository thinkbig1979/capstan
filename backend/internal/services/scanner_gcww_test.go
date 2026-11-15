package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// agent-os-gcww F4b: scanDirectoryWithRoot decided whether to delete rows for
// removed compose files on len(seenNames), which is set before the
// empty-project-name skip. A folder whose only remaining compose file is
// skipped registered nothing this pass, yet still ran the delete. Producer:
// seenNames in scanDirectoryWithRoot. Consumer: the
// deleteRowsForRemovedComposeFiles call after the loop.
func TestScannerGcww_FolderWhoseOnlyFileIsSkippedKeepsItsRows(t *testing.T) {
	root := t.TempDir()
	// "__" normalises to an empty compose project name, so the default file
	// is skipped; the named file pins `name:` and registers.
	dir := filepath.Join(root, "__")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), unnamedCompose, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.shop.yaml"), composeNamed("shop"), 0o644))
	_, s := newTestWatcher(t, root)
	s.SetContainerLister(&fakeContainerLister{})
	require.NoError(t, s.ScanDirectory(dir))
	require.Equal(t, []string{"compose.shop.yaml"}, composeFilesIn(t, s, dir), "control: only the named file registers")

	require.NoError(t, os.Remove(filepath.Join(dir, "compose.shop.yaml")))
	require.NoError(t, s.ScanDirectory(dir))
	require.Equal(t, []string{"compose.shop.yaml"}, composeFilesIn(t, s, dir),
		"a pass that registered no stack from the folder must not delete its rows")
}
