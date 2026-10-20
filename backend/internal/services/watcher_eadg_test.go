package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-eadg: the watcher decided "is this a compose file" with its own
// prefix check, narrower than the patterns the scanner registers stacks from,
// so a stack named compose-prod.yaml was registered but its edits never
// triggered a rescan. The scanner is the producer of the set; the watcher's
// shouldProcessEvent is a consumer, so the oracle here is the scanner itself.
func TestWatcherService_Eadg_EveryScannerRegisteredNameTriggersARescan(t *testing.T) {
	names := []string{
		"compose.yaml",
		"compose.yml",
		"docker-compose.yaml",
		"docker-compose.yml",
		"compose.api.yaml",
		"composer.yml",
		"compose-prod.yaml",
		"compose-prod.yml",
		"docker-compose-dev.yaml",
		"docker-compose-dev.yml",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			stackDir := filepath.Join(dir, "web")
			require.NoError(t, os.Mkdir(stackDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(stackDir, name),
				[]byte("services:\n  web:\n    image: nginx\n"), 0o644))

			w, scanner := newTestWatcher(t, dir)
			require.NoError(t, scanner.ScanDirectory(stackDir))

			stacks, err := scanner.db.ListStacks()
			require.NoError(t, err)
			require.NotEmpty(t, stacks, "control: the scanner must register %s, or this case proves nothing", name)

			assert.True(t, w.shouldProcessEvent(filepath.Join(stackDir, name)),
				"%s is registered as a stack, so an edit to it must trigger a rescan", name)
		})
	}
}

func TestWatcherService_Eadg_NonComposeNamesStillIgnored(t *testing.T) {
	w, _ := newTestWatcher(t, t.TempDir())

	for _, name := range []string{
		"compose-prod.txt",
		"compose-prod.yaml.bak",
		"docker-compose-dev.json",
		"my-compose.yaml",
		"values.yaml",
		"README.md",
	} {
		assert.False(t, w.shouldProcessEvent(filepath.Join("/srv/stacks/web", name)), name)
	}
}
