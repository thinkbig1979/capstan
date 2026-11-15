package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
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
	s.SetContainerLister(&fakeContainerLister{})
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

// fakeContainerLister answers GetContainerListCtx from containers/err, or
// blocks until release is closed or ctx ends when release is set, the way a
// real client gives up at its deadline. calls counts lookups.
type fakeContainerLister struct {
	mu         sync.Mutex
	containers []models.Container
	err        error
	release    chan struct{}
	calls      int
}

func (f *fakeContainerLister) GetContainerListCtx(ctx context.Context, _ string) ([]models.Container, error) {
	f.mu.Lock()
	f.calls++
	release := f.release
	f.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.containers, f.err
}

func (f *fakeContainerLister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func statusOf(t *testing.T, s *ScannerService, dir, file string) string {
	t.Helper()
	rows, err := s.db.ListStacksByDirectory(dir)
	require.NoError(t, err)
	for _, r := range rows {
		if r.ComposeFile == file {
			return r.Status
		}
	}
	return ""
}

// review F3: a row whose file is gone but whose project still has containers
// is kept and marked error, as Delete refuses to hide those containers.
func TestScannerEpc5_GoneFileWithContainersKeepsTheRowAsError(t *testing.T) {
	app, s := epc5Folder(t)
	s.SetContainerLister(&fakeContainerLister{containers: []models.Container{{Name: "app-worker-1"}}})
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app))
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.worker.yaml"))
}

func TestScannerEpc5_GoneFileWithFailedLookupKeepsTheRowAsError(t *testing.T) {
	app, s := epc5Folder(t)
	s.SetContainerLister(&fakeContainerLister{err: errors.New("docker daemon unreachable")})
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app))
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.worker.yaml"))
}

func TestScannerEpc5_GoneFileWithNoListerKeepsTheRowAsError(t *testing.T) {
	app, s := epc5Folder(t)
	s.SetContainerLister(nil)
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app))
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.worker.yaml"))
}

// A hung daemon costs one bound per pass: after the first lookup times out,
// the pass keeps every remaining candidate without asking again.
func TestScannerEpc5_LookupTimeoutKeepsEveryRowAndWaitsOnce(t *testing.T) {
	old := containerLookupBound
	containerLookupBound = 200 * time.Millisecond
	t.Cleanup(func() { containerLookupBound = old })

	app, s := epc5Folder(t)
	for _, name := range []string{"compose.a.yaml", "compose.b.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(app, name), composeNamed(name), 0o644))
	}
	require.NoError(t, s.ScanDirectory(app))
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	lister := &fakeContainerLister{release: release}
	s.SetContainerLister(lister)
	for _, name := range []string{"compose.a.yaml", "compose.b.yaml"} {
		require.NoError(t, os.Remove(filepath.Join(app, name)))
	}

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.a.yaml", "compose.b.yaml", "compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app),
		"a timed out lookup keeps both candidates")
	require.Equal(t, 1, lister.callCount(), "the second candidate must not wait on the hung daemon again")
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.a.yaml"))
	require.Equal(t, string(StackStatusError), statusOf(t, s, app, "compose.b.yaml"))
}

// A timeout is remembered for one pass only: the next pass asks again. One
// test per entry point that starts a pass (review G1).
func timedOutThenClearedPass(t *testing.T, scan func(s *ScannerService, app string)) {
	t.Helper()
	old := containerLookupBound
	containerLookupBound = 200 * time.Millisecond
	t.Cleanup(func() { containerLookupBound = old })

	app, s := epc5Folder(t)
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))
	release := make(chan struct{})
	hung := &fakeContainerLister{release: release}
	s.SetContainerLister(hung)

	scan(s, app)
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app), "control: the timed out pass keeps the row")
	require.Equal(t, 1, hung.callCount(), "control: the first pass asked once")

	close(release)
	answering := &fakeContainerLister{}
	s.SetContainerLister(answering)

	scan(s, app)
	require.Equal(t, 1, answering.callCount(), "the next pass must ask again, not reuse the earlier pass's timeout")
	require.Equal(t, []string{"compose.yaml"}, composeFilesIn(t, s, app), "with no containers the next pass deletes the row")
}

func TestScannerEpc5_ScanDirectoryAsksAgainAfterAnEarlierTimeout(t *testing.T) {
	timedOutThenClearedPass(t, func(s *ScannerService, app string) {
		require.NoError(t, s.ScanDirectory(app))
	})
}

func TestScannerEpc5_ScanAllAsksAgainAfterAnEarlierTimeout(t *testing.T) {
	timedOutThenClearedPass(t, func(s *ScannerService, _ string) {
		_, err := s.ScanAll()
		require.NoError(t, err)
	})
}
