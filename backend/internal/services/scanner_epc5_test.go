package services

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// agent-os-epc5: a compose file removed from a folder that still holds
// another stack left its row behind forever: scanDirectoryWithRoot only
// upserts, and pruneStaleStacks deletes rows only for folders that are gone.
// Producer: a compose file disappearing while its folder lives on. Consumer:
// scanDirectoryWithRoot's per-folder upsert loop.

func composeFilesIn(t *testing.T, s *ScannerService, dir string) []string {
	t.Helper()
	rows, err := s.db.ListStacksByDirectory(dir)
	require.NoError(t, err)
	files := make([]string, 0, len(rows))
	for _, r := range rows {
		files = append(files, r.ComposeFile)
	}
	sort.Strings(files)
	return files
}

func epc5Folder(t *testing.T) (string, *ScannerService) {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.yaml"), composeNamed("main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.worker.yaml"), composeNamed("worker"), 0o644))
	_, s := newTestWatcher(t, root)
	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app), "control: both stacks registered")
	return app, s
}

func TestScannerEpc5_RemovedComposeFileRowIsDeletedOnRescan(t *testing.T) {
	app, s := epc5Folder(t)
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, app), "only the removed file's row goes")
}

// A folder whose compose files are ALL gone (an unmounted disk's empty
// mountpoint, a checkout mid-way) keeps every row: nothing in this pass proves
// the folder is the one the rows were registered from.
func TestScannerEpc5_FolderWithNoComposeFileLeftKeepsItsRows(t *testing.T) {
	app, s := epc5Folder(t)
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))
	require.NoError(t, os.Remove(filepath.Join(app, "compose.yaml")))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app))
}

// A dangling symlink is still present (Lstat finds it), so its row stays.
func TestScannerEpc5_DanglingSymlinkKeepsItsRow(t *testing.T) {
	app, s := epc5Folder(t)
	worker := filepath.Join(app, "compose.worker.yaml")
	require.NoError(t, os.Remove(worker))
	require.NoError(t, os.Symlink(filepath.Join(app, "missing.yaml"), worker))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app))
}

// ScanAll's walk takes the same path.
func TestScannerEpc5_ScanAllDeletesTheRemovedFilesRow(t *testing.T) {
	app, s := epc5Folder(t)
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))

	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, app))
}
