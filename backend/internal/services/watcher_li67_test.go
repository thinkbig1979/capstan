package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// agent-os-li67 (review F7 on #746): when the scan depth setting cannot be
// read, the watcher still starts, at the default depth, and says so at WARN.
// Producer: watchDepth's error branch. Consumer: the depth every watchDir
// call uses.
func TestWatcherLi67_UnreadableScanDepthWatchesAtDepth1WithAWarn(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("scan_depth", "3"))
	cfg := &config.Config{StacksDir: t.TempDir(), DataDir: t.TempDir()}
	w := NewWatcherService(NewScannerService(cfg, db), cfg)
	require.Equal(t, 3, w.watchDepth(), "control: a readable setting is followed")

	buf := captureSlog(t)
	hide()
	depth := w.watchDepth()
	restore()

	assert.Equal(t, 1, depth, "an unreadable scan depth falls back to the default, 1")
	out := buf.String()
	assert.Contains(t, out, "level=WARN", "the fallback must be logged at WARN")
	assert.Contains(t, out, "no such table: settings", "the WARN must carry the cause")
}
