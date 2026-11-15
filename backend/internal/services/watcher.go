package services

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/thinkbig1979/capstan/backend/internal/config"
)

const DEBOUNCE_DELAY = 500 * time.Millisecond

type WatcherService struct {
	scanner *ScannerService
	config  *config.Config
	watcher *fsnotify.Watcher
	timers  map[string]*time.Timer
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewWatcherService(scanner *ScannerService, cfg *config.Config) *WatcherService {
	ctx, cancel := context.WithCancel(context.Background())

	return &WatcherService{
		scanner: scanner,
		config:  cfg,
		timers:  make(map[string]*time.Timer),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (w *WatcherService) Start() error {
	allDirs := w.config.GetAllStacksDirs()
	slog.Info("Starting file watcher service", "stacksDirs", allDirs)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	if err := w.addWatchPaths(); err != nil {
		watcher.Close()
		return err
	}

	go w.watchLoop()

	slog.Info("File watcher service started")
	return nil
}

func (w *WatcherService) addWatchPaths() error {
	allDirs := w.config.GetAllStacksDirs()
	maxDepth := w.watchDepth()

	for _, stacksDir := range allDirs {
		if err := w.watcher.Add(stacksDir); err != nil {
			slog.Warn("Failed to watch stacks directory", "path", stacksDir, "error", err)
		}

		if _, err := os.ReadDir(stacksDir); err != nil {
			slog.Warn("Failed to read stacks directory", "path", stacksDir, "error", err)
			continue
		}
		w.watchSubdirs(stacksDir, maxDepth, 1, false)
	}

	return nil
}

// watchDepth is how many levels below each stacks dir the watcher covers: the
// scanner's own scan depth, so every folder the scanner can register a stack
// from is watched (agent-os-t03q). The scanner refuses to scan when the
// setting cannot be read; the watcher still starts, at the default depth.
// It is read at boot and per runtime folder, so raising scan_depth covers
// already existing deeper folders only after a restart.
func (w *WatcherService) watchDepth() int {
	depth, err := w.scanner.readScanDepth()
	if err != nil {
		slog.Warn("Watching at the default depth: the scan depth setting could not be read", "depth", defaultScanDepth, "error", err)
		return defaultScanDepth
	}
	return depth
}

// watchSubdirs watches the folders below dir, which sits depth-1 levels below
// a stacks dir, down to maxDepth, with the scanner's walk rules
// (scanDirectoryRecursive): directories only (DirEntry.IsDir is false for a
// symlink) and no dot names. With rescan set, each folder that already holds
// a compose or env file is rescanned: it arrived with a folder created or
// moved in at runtime, and its files fired no event this watcher could see.
func (w *WatcherService) watchSubdirs(dir string, maxDepth, depth int, rescan bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("Failed to read directory to watch", "path", dir, "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		w.watchDir(filepath.Join(dir, entry.Name()), maxDepth, depth, rescan)
	}
}

// watchDir watches path, depth levels below a stacks dir, and its subfolders
// down to maxDepth.
func (w *WatcherService) watchDir(path string, maxDepth, depth int, rescan bool) {
	if err := w.watcher.Add(path); err != nil {
		slog.Warn("Failed to watch subdirectory", "path", path, "error", err)
		return
	}
	if rescan {
		w.rescanIfItHoldsStackFiles(path)
	}
	if depth < maxDepth {
		w.watchSubdirs(path, maxDepth, depth+1, rescan)
	}
}

func (w *WatcherService) watchLoop() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("Watcher error", "error", err)

		case <-w.ctx.Done():
			return
		}
	}
}

func (w *WatcherService) handleEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Rename == fsnotify.Rename {
		w.unwatchTree(event.Name)
	}

	// A new directory is handled BEFORE the filename filter: the filter is for
	// files, and a folder named `newstack` fails it, so it was never watched
	// and edits inside it triggered no rescan (agent-os-3uu8).
	if event.Op&fsnotify.Create == fsnotify.Create {
		info, err := os.Stat(event.Name) //geterrors:ignore a Create event for a path that is already gone stats with an error, and the only question here is "is this a new directory to watch"
		if err == nil && info.IsDir() {
			w.watchNewDir(event.Name)
			return
		}
	}

	if !w.shouldProcessEvent(event.Name) {
		return
	}

	dirPath := filepath.Dir(event.Name)

	isRootDir := false
	for _, stacksDir := range w.config.GetAllStacksDirs() {
		if dirPath == stacksDir {
			isRootDir = true
			break
		}
	}

	if !isRootDir && !strings.HasPrefix(filepath.Base(dirPath), ".") {
		w.scheduleRescan(dirPath)
	}
}

// watchNewDir adds a directory created at runtime under the same rule the boot
// walk applies in addWatchPaths: 1 to scan-depth levels below a configured
// stacks dir, with no dot folder on the way. Its subfolders, down to the same
// depth, are watched with it (a mkdir -p or a mv in brings them along).
// The configured stacks dirs are set at boot and a settings change to them is
// applied at the next boot, never written into a running Config (safe-defaults
// rule 14), so this membership check and addWatchPaths read the same list.
func (w *WatcherService) watchNewDir(path string) {
	// A symlink to a directory is skipped, as the boot walk (DirEntry.IsDir is
	// false for a symlink), the scanner and the prune walk all skip it; watched
	// here it would register a stack the next full scan prunes.
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink != 0 { //geterrors:ignore a path that vanished since the Create event is simply not a folder to watch
		return
	}
	maxDepth := w.watchDepth()
	depth := w.depthBelowStacksDir(path, maxDepth)
	if depth == 0 {
		return
	}

	slog.Info("New subdirectory detected, adding to watcher", "path", path)
	w.watchDir(path, maxDepth, depth, true)
}

// depthBelowStacksDir returns how many levels path sits below the nearest
// configured stacks dir, or 0 when no stacks dir is within maxDepth levels
// above it or a folder on the way (path's own name included) starts with ".",
// which the scanner never enters.
func (w *WatcherService) depthBelowStacksDir(path string, maxDepth int) int {
	p := filepath.Clean(path)
	for depth := 1; depth <= maxDepth; depth++ {
		if strings.HasPrefix(filepath.Base(p), ".") {
			return 0
		}
		parent := filepath.Dir(p)
		for _, stacksDir := range w.config.GetAllStacksDirs() {
			if filepath.Clean(stacksDir) == parent {
				return depth
			}
		}
		if parent == p {
			return 0
		}
		p = parent
	}
	return 0
}

// rescanIfItHoldsStackFiles schedules a rescan of a folder that was just
// watched, when it already holds a compose or env file. A file written between
// the mkdir and the Add fired no event this watcher could see. An empty folder
// is left alone: a rescan would register a directory row for it.
func (w *WatcherService) rescanIfItHoldsStackFiles(path string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		slog.Warn("Failed to read new subdirectory", "path", path, "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && w.shouldProcessEvent(entry.Name()) {
			w.scheduleRescan(path)
			return
		}
	}
}

// unwatchTree drops the watch on a path that was renamed away, and on every
// watched path below it, before the paired Create for the new name is handled
// (agent-os-cglx). A moved folder keeps its inode, so inotify hands Add(new)
// the old watch descriptor; fsnotify then reuses the old entry, and its own
// IN_MOVE_SELF cleanup later removes that shared kernel watch, leaving the new
// name unwatched. fsnotify delivers the Rename for the old name before it
// reads the Create for the new one (the Events channel is unbuffered), so
// removing here always runs first and the Add gets a fresh descriptor. A
// renamed file, or a path fsnotify already dropped, is not watched: that
// ErrNonExistentWatch is expected.
func (w *WatcherService) unwatchTree(path string) {
	path = filepath.Clean(path)
	for _, watched := range w.watcher.WatchList() {
		if !isSameOrBelow(watched, path) {
			continue
		}
		if err := w.watcher.Remove(watched); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			slog.Warn("Failed to unwatch renamed directory", "path", watched, "error", err)
		}
	}
}

// isSameOrBelow reports whether p is dir or one of its descendants, by walking
// p's parents. It compares the watcher's own stored watch paths, not a
// containment decision on user input: the renamed-away path no longer exists,
// so a resolving check (pathutil.IsContained) cannot answer.
func isSameOrBelow(p, dir string) bool {
	for p = filepath.Clean(p); ; p = filepath.Dir(p) {
		if p == dir {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

func (w *WatcherService) shouldProcessEvent(filePath string) bool {
	filename := filepath.Base(filePath)

	// isComposeFileName is the scanner's own matcher: a name the scanner
	// registers a stack from must also trigger a rescan (agent-os-eadg).
	return isComposeFileName(filename) || strings.HasPrefix(filename, ".env")
}

func (w *WatcherService) scheduleRescan(dirPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if timer, exists := w.timers[dirPath]; exists {
		timer.Stop()
	}

	w.timers[dirPath] = time.AfterFunc(DEBOUNCE_DELAY, func() {
		w.performRescan(dirPath)

		w.mu.Lock()
		delete(w.timers, dirPath)
		w.mu.Unlock()
	})
}

func (w *WatcherService) performRescan(dirPath string) {
	slog.Info("Rescanning directory due to file change", "path", dirPath)

	if err := w.scanner.ScanDirectory(dirPath); err != nil {
		slog.Error("Failed to rescan directory", "path", dirPath, "error", err)
	} else {
		slog.Info("Directory rescanned successfully", "path", dirPath)
	}
}

func (w *WatcherService) Stop() {
	slog.Info("Stopping file watcher service")

	w.cancel()

	if w.watcher != nil {
		w.watcher.Close()
	}

	w.mu.Lock()
	for _, timer := range w.timers {
		timer.Stop()
	}
	w.timers = make(map[string]*time.Timer)
	w.mu.Unlock()

	slog.Info("File watcher service stopped")
}
