package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// WatcherService is started from cmd/server/main.go:316 and measured 0.0% of
// 86 statements (agent-os-wo9x). These tests drive it against a real temp
// directory and a real fsnotify watcher — the debounce and the event filter
// are the parts that decide whether a compose edit is ever noticed.

func newTestWatcher(t *testing.T, stacksDir string, extra ...string) (*WatcherService, *ScannerService) {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{
		StacksDir:       stacksDir,
		ExtraStacksDirs: extra,
		DataDir:         t.TempDir(),
	}
	scanner := NewScannerService(cfg, db)
	w := NewWatcherService(scanner, cfg)
	t.Cleanup(w.Stop)
	return w, scanner
}

func TestWatcherService_ShouldProcessEvent(t *testing.T) {
	w, _ := newTestWatcher(t, t.TempDir())

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"compose.yml", "/srv/stacks/web/compose.yml", true},
		{"compose.yaml", "/srv/stacks/web/compose.yaml", true},
		{"docker-compose.yml", "/srv/stacks/web/docker-compose.yml", true},
		{"docker-compose.yaml", "/srv/stacks/web/docker-compose.yaml", true},
		{"compose.override.yml", "/srv/stacks/web/compose.override.yml", true},
		{".env", "/srv/stacks/web/.env", true},
		{".env.local", "/srv/stacks/web/.env.local", true},
		// A compose file must carry a yaml/yml extension.
		{"compose.txt", "/srv/stacks/web/compose.txt", false},
		{"compose.yml.bak", "/srv/stacks/web/compose.yml.bak", false},
		// The prefix must be followed by a dot, so these are not compose files.
		{"composer.yml", "/srv/stacks/web/composer.yml", false},
		{"bare compose", "/srv/stacks/web/compose", false},
		{"unrelated yaml", "/srv/stacks/web/values.yaml", false},
		{"env without dot prefix", "/srv/stacks/web/env", false},
		{"readme", "/srv/stacks/web/README.md", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, w.shouldProcessEvent(tc.path))
		})
	}
}

func TestWatcherService_StartAndStop(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "web"), 0o755))

	w, _ := newTestWatcher(t, dir)

	require.NoError(t, w.Start())
	assert.NotNil(t, w.watcher)

	w.Stop()
	// Stop cancels the context, which is what ends watchLoop.
	assert.Error(t, w.ctx.Err())
}

func TestWatcherService_StopIsSafeBeforeStart(t *testing.T) {
	w, _ := newTestWatcher(t, t.TempDir())

	// main.go defers Stop unconditionally, so a failed Start must not make
	// Stop panic on the nil watcher.
	assert.NotPanics(t, w.Stop)
}

func TestWatcherService_StopIsIdempotent(t *testing.T) {
	w, _ := newTestWatcher(t, t.TempDir())
	require.NoError(t, w.Start())

	w.Stop()
	assert.NotPanics(t, w.Stop)
}

func TestWatcherService_StartWatchesSubdirectoriesButNotHiddenOnes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "web"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "loose.txt"), []byte("x"), 0o644))

	w, _ := newTestWatcher(t, dir)
	require.NoError(t, w.Start())

	watched := w.watcher.WatchList()
	assert.Contains(t, watched, dir)
	assert.Contains(t, watched, filepath.Join(dir, "web"))
	assert.NotContains(t, watched, filepath.Join(dir, ".git"),
		"dot-directories are skipped so .git churn cannot trigger rescans")
	assert.NotContains(t, watched, filepath.Join(dir, "loose.txt"),
		"only directories are added")
}

func TestWatcherService_StartWatchesEveryConfiguredRoot(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(second, "api"), 0o755))

	w, _ := newTestWatcher(t, first, second)
	require.NoError(t, w.Start())

	watched := w.watcher.WatchList()
	assert.Contains(t, watched, first)
	assert.Contains(t, watched, second)
	assert.Contains(t, watched, filepath.Join(second, "api"))
}

func TestWatcherService_StartSurvivesAMissingStacksDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	w, _ := newTestWatcher(t, missing)

	// addWatchPaths logs and continues rather than failing — a misconfigured
	// extra directory must not stop the server from booting.
	assert.NoError(t, w.Start())
}

func TestWatcherService_ScheduleRescanDebouncesBurstsIntoOneScan(t *testing.T) {
	dir := t.TempDir()
	stackDir := filepath.Join(dir, "web")
	require.NoError(t, os.Mkdir(stackDir, 0o755))

	w, _ := newTestWatcher(t, dir)

	// Three edits in quick succession — a single save from an editor often
	// produces several — must collapse into one rescan.
	w.scheduleRescan(stackDir)
	w.scheduleRescan(stackDir)
	w.scheduleRescan(stackDir)

	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 1, pending, "the burst must leave exactly one pending timer")

	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.timers) == 0
	}, 5*time.Second, 20*time.Millisecond, "the timer must clear itself after firing")
}

func TestWatcherService_ScheduleRescanTracksDirectoriesIndependently(t *testing.T) {
	dir := t.TempDir()
	w, _ := newTestWatcher(t, dir)

	w.scheduleRescan(filepath.Join(dir, "web"))
	w.scheduleRescan(filepath.Join(dir, "api"))

	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 2, pending, "two directories debounce separately")
}

func TestWatcherService_StopCancelsPendingRescans(t *testing.T) {
	dir := t.TempDir()
	w, _ := newTestWatcher(t, dir)

	w.scheduleRescan(filepath.Join(dir, "web"))
	w.Stop()

	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 0, pending, "Stop drops every pending timer")
}

func TestWatcherService_PerformRescanRegistersTheDirectory(t *testing.T) {
	dir := t.TempDir()
	stackDir := filepath.Join(dir, "web")
	require.NoError(t, os.Mkdir(stackDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(stackDir, "compose.yml"),
		[]byte("services:\n  web:\n    image: nginx\n"),
		0o644,
	))

	w, scanner := newTestWatcher(t, dir)
	w.performRescan(stackDir)

	stacks, err := scanner.db.ListStacks()
	require.NoError(t, err)
	assert.NotEmpty(t, stacks, "the rescan should have discovered the compose file")
}

func TestWatcherService_PerformRescanSwallowsAScanFailure(t *testing.T) {
	dir := t.TempDir()
	w, _ := newTestWatcher(t, dir)

	// A directory that no longer exists — the watcher must log and carry on
	// rather than take the watch loop down with it.
	assert.NotPanics(t, func() {
		w.performRescan(filepath.Join(dir, "vanished"))
	})
}

func TestWatcherService_HandleEventIgnoresUninterestingFiles(t *testing.T) {
	dir := t.TempDir()
	stackDir := filepath.Join(dir, "web")
	require.NoError(t, os.Mkdir(stackDir, 0o755))

	w, _ := newTestWatcher(t, dir)
	require.NoError(t, w.Start())

	w.handleEvent(fsnotifyWrite(filepath.Join(stackDir, "README.md")))

	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 0, pending, "a non-compose file schedules nothing")
}

func TestWatcherService_HandleEventSchedulesRescanForAComposeEdit(t *testing.T) {
	dir := t.TempDir()
	stackDir := filepath.Join(dir, "web")
	require.NoError(t, os.Mkdir(stackDir, 0o755))

	w, _ := newTestWatcher(t, dir)
	require.NoError(t, w.Start())

	w.handleEvent(fsnotifyWrite(filepath.Join(stackDir, "compose.yml")))

	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 1, pending)
}

func TestWatcherService_HandleEventIgnoresAComposeFileAtTheRoot(t *testing.T) {
	dir := t.TempDir()

	w, _ := newTestWatcher(t, dir)
	require.NoError(t, w.Start())

	// A compose file directly in a configured root is not a stack directory,
	// so rescanning the root itself would be wrong.
	w.handleEvent(fsnotifyWrite(filepath.Join(dir, "compose.yml")))

	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 0, pending)
}

func TestWatcherService_HandleEventAddsANewSubdirectoryToTheWatch(t *testing.T) {
	dir := t.TempDir()

	w, _ := newTestWatcher(t, dir)
	require.NoError(t, w.Start())

	// handleEvent bails out in shouldProcessEvent before it ever reaches the
	// add-a-watch branch, so that branch is only reachable for a path that is
	// named like a compose file or an .env — hence the odd directory name.
	// A normal new stack directory is picked up instead via the compose-file
	// event inside it, which the parent root watch delivers (see the test
	// below).
	newDir := filepath.Join(dir, "compose.yml")
	require.NoError(t, os.Mkdir(newDir, 0o755))

	w.handleEvent(fsnotifyCreate(newDir))

	assert.Contains(t, w.watcher.WatchList(), newDir,
		"a newly created stack directory must start being watched immediately")
	w.mu.Lock()
	pending := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 0, pending, "adding a watch is not itself a reason to rescan")
}

// fsnotify events carry an Op bitmask; these keep the call sites readable.
func fsnotifyWrite(path string) fsnotify.Event {
	return fsnotify.Event{Name: path, Op: fsnotify.Write}
}

func fsnotifyCreate(path string) fsnotify.Event {
	return fsnotify.Event{Name: path, Op: fsnotify.Create}
}

func TestWatcherService_HandleEventSchedulesRescanForANewStackDirectory(t *testing.T) {
	dir := t.TempDir()

	w, _ := newTestWatcher(t, dir)
	require.NoError(t, w.Start())

	// The realistic path for a brand-new stack: the directory is created (and
	// is NOT itself compose-shaped, so no watch is added for it), then a
	// compose file appears inside it. The parent root watch delivers that
	// event, and its directory is not a configured root, so it rescans.
	stackDir := filepath.Join(dir, "brand-new")
	require.NoError(t, os.Mkdir(stackDir, 0o755))

	w.handleEvent(fsnotifyCreate(stackDir))
	w.mu.Lock()
	afterDirCreate := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 0, afterDirCreate, "the directory itself is not compose-shaped")

	w.handleEvent(fsnotifyCreate(filepath.Join(stackDir, "compose.yml")))
	w.mu.Lock()
	afterFileCreate := len(w.timers)
	w.mu.Unlock()
	assert.Equal(t, 1, afterFileCreate, "the compose file inside it does schedule a rescan")
}
