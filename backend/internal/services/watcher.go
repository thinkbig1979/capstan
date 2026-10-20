package services

import (
	"context"
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

	for _, stacksDir := range allDirs {
		if err := w.watcher.Add(stacksDir); err != nil {
			slog.Warn("Failed to watch stacks directory", "path", stacksDir, "error", err)
		}

		entries, err := os.ReadDir(stacksDir)
		if err != nil {
			slog.Warn("Failed to read stacks directory", "path", stacksDir, "error", err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			dirPath := filepath.Join(stacksDir, entry.Name())
			if err := w.watcher.Add(dirPath); err != nil {
				slog.Warn("Failed to watch subdirectory", "path", dirPath, "error", err)
			}
		}
	}

	return nil
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
// walk applies in addWatchPaths: a direct child of a configured stacks dir,
// not starting with ".". Deeper and hidden folders were never watched at boot.
// The configured stacks dirs are set at boot and a settings change to them is
// applied at the next boot, never written into a running Config (safe-defaults
// rule 14), so this membership check and addWatchPaths read the same list.
func (w *WatcherService) watchNewDir(path string) {
	if strings.HasPrefix(filepath.Base(path), ".") {
		return
	}
	parent := filepath.Clean(filepath.Dir(path))
	isStackFolder := false
	for _, stacksDir := range w.config.GetAllStacksDirs() {
		if filepath.Clean(stacksDir) == parent {
			isStackFolder = true
			break
		}
	}
	if !isStackFolder {
		return
	}

	slog.Info("New subdirectory detected, adding to watcher", "path", path)
	if err := w.watcher.Add(path); err != nil {
		slog.Warn("Failed to watch new subdirectory", "path", path, "error", err)
		return
	}

	// A file written between the mkdir and the Add above fired no event this
	// watcher could see, so look once for what is already inside. An empty
	// folder is left alone: a rescan would register a directory row for it.
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
