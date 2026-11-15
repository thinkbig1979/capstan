package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-jny5: pruneStaleStacks deleted the rows of a folder that is gone
// with no container check, so `mv app app-old` while app runs hid its
// containers from the stack list. Producer: the folder-gone prune
// (DeleteDirectory, whose cascade takes the stacks, and DeleteStack).
// Consumer: the stacks table and the stack list.

var unnamedCompose = []byte("services:\n  web:\n    image: nginx\n")

// renamedAwayFolder registers root/app from compose, renames it to
// root/app-old, and returns the scanner with lister installed.
func renamedAwayFolder(t *testing.T, compose []byte, lister projectContainerLister) (root, app, moved string, s *ScannerService) {
	t.Helper()
	root = t.TempDir()
	app = filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.yaml"), compose, 0o644))
	_, s = newTestWatcher(t, root)
	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, app), "control: the stack is registered")
	moved = filepath.Join(root, "app-old")
	require.NoError(t, os.Rename(app, moved))
	s.containers = lister
	return root, app, moved, s
}

func directoryRowExists(t *testing.T, s *ScannerService, path string) bool {
	t.Helper()
	dirs, err := s.db.ListDirectories()
	require.NoError(t, err)
	for _, d := range dirs {
		if d.Path == path {
			return true
		}
	}
	return false
}

func TestScannerJny5_RenamedAwayFolderWithContainersKeepsItsRowAsError(t *testing.T) {
	_, app, _, s := renamedAwayFolder(t, unnamedCompose, &fakeContainerLister{containers: []models.Container{{Name: "app-web-1"}}})

	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, app), "the gone folder's row must stay while its project has containers")
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.yaml"))
	require.True(t, directoryRowExists(t, s, app), "the directory row must stay too: deleting it takes the stacks by cascade")
}

func TestScannerJny5_RenamedAwayFolderWithFailedLookupKeepsItsRowAsError(t *testing.T) {
	_, app, _, s := renamedAwayFolder(t, unnamedCompose, &fakeContainerLister{err: errors.New("docker daemon unreachable")})

	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, app), "a lookup that failed must keep the row")
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.yaml"))
}

func TestScannerJny5_RenamedAwayFolderWithNoContainersIsDeleted(t *testing.T) {
	_, app, moved, s := renamedAwayFolder(t, unnamedCompose, &fakeContainerLister{})

	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Empty(t, composeFilesIn(t, s, app), "with no containers the gone folder's row goes")
	require.False(t, directoryRowExists(t, s, app))
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, moved), "control: the new folder is registered")
}

// D2: when the compose file pins `name:`, the renamed folder's new row carries
// the same project. Its containers stay visible through that row, so the old
// row goes; keeping it would make every mutating compose op on the new row
// refuse as a shared project name.
func TestScannerJny5_RenamedAwayFolderWhoseProjectAnotherRowShowsIsDeleted(t *testing.T) {
	_, app, moved, s := renamedAwayFolder(t, composeNamed("shop"), &fakeContainerLister{containers: []models.Container{{Name: "shop-web-1"}}})

	_, err := s.ScanAll()
	require.NoError(t, err)
	require.Empty(t, composeFilesIn(t, s, app), "the old row must go: the new folder's row shows project shop's containers")
	rows := composeFilesIn(t, s, moved)
	require.Equal(t, []string{"compose.yaml"}, rows)

	newRows, err := s.db.ListStacksByDirectory(moved)
	require.NoError(t, err)
	d := &DockerService{stackLookup: s.db}
	require.NoError(t, d.refuseSharedProjectName(newRows[0]), "the new row's compose ops must not be refused as a shared project")
}

// D2 on the epc5 path: a compose file renamed in place keeps its `name:`, so
// the old file's row shares the project with the new file's row and goes.
func TestScannerJny5_GoneFileWhoseProjectAnotherRowShowsIsDeleted(t *testing.T) {
	app, s := epc5Folder(t)
	s.SetContainerLister(&fakeContainerLister{containers: []models.Container{{Name: "worker-web-1"}}})
	require.NoError(t, os.Rename(filepath.Join(app, "compose.worker.yaml"), filepath.Join(app, "compose.jobs.yaml")))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.jobs.yaml", "compose.yaml"}, composeFilesIn(t, s, app),
		"the renamed file's old row must go: compose.jobs.yaml's row shows project worker's containers")
}
