package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// newDefaultDirTestHandler builds a SettingsHandler over two real temp roots,
// so pathutil can resolve them, and returns the shared *config.Config the
// handler was given.
func newDefaultDirTestHandler(t *testing.T) (*database.DB, *config.Config, *gin.Engine) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	primary := filepath.Join(t.TempDir(), "stacks")
	extra := filepath.Join(t.TempDir(), "more-stacks")
	require.NoError(t, os.MkdirAll(primary, 0o755))
	require.NoError(t, os.MkdirAll(extra, 0o755))

	cfg := &config.Config{StacksDir: primary, ExtraStacksDirs: []string{extra}}
	handler := NewSettingsHandler(db, primary, "test-secret-key-32-chars-long!!!", false, nil, cfg)
	return db, cfg, setupSettingsFullRouter(handler)
}

func putDefaultDir(t *testing.T, router *gin.Engine, dir string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"defaultDir": dir})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/settings/directories", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// agent-os-a1ye.6 (a): the PUT persists the choice but never mutates the
// shared config, which every scanner/watcher/handler goroutine reads without
// a lock. The change is reported as pending until a restart applies it.
func TestUpdateConfiguredDirectories_PersistsWithoutMutatingConfig(t *testing.T) {
	db, cfg, router := newDefaultDirTestHandler(t)
	primary, extra := cfg.StacksDir, cfg.ExtraStacksDirs[0]

	w := putDefaultDir(t, router, extra)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assert.Equal(t, primary, cfg.StacksDir, "PUT must not write the live config")
	assert.Equal(t, []string{primary, extra}, cfg.GetAllStacksDirs(), "the root set must be unchanged")

	stored, err := db.GetSetting("default_stacks_dir")
	require.NoError(t, err)
	assert.Equal(t, extra, stored)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, primary, resp["active"])
	assert.Equal(t, extra, resp["pending"])
	assert.Equal(t, true, resp["restartRequired"])
}

// agent-os-a1ye.6: choosing the active root again clears the pending change.
func TestUpdateConfiguredDirectories_ActiveRootIsNotPending(t *testing.T) {
	_, cfg, router := newDefaultDirTestHandler(t)

	w := putDefaultDir(t, router, cfg.StacksDir)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, cfg.StacksDir, resp["active"])
	assert.Equal(t, cfg.StacksDir, resp["pending"])
	assert.Equal(t, false, resp["restartRequired"])
}

// agent-os-a1ye.6 (c): a path lexically inside a root whose symlink resolves
// outside every root is rejected, and nothing is created through it.
func TestUpdateConfiguredDirectories_RejectsSymlinkEscape(t *testing.T) {
	db, cfg, router := newDefaultDirTestHandler(t)

	outside := t.TempDir()
	link := filepath.Join(cfg.StacksDir, "escape")
	require.NoError(t, os.Symlink(outside, link))

	w := putDefaultDir(t, router, filepath.Join(link, "new"))
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	_, statErr := os.Stat(filepath.Join(outside, "new"))
	assert.True(t, os.IsNotExist(statErr), "MkdirAll must not run through the symlink")

	stored, err := db.GetSetting("default_stacks_dir")
	require.NoError(t, err)
	assert.Equal(t, "", stored, "a rejected path must not be persisted")
}

// agent-os-a1ye.6: only a configured root can become the default. A
// subdirectory of a root would become a nested root at the next boot.
func TestUpdateConfiguredDirectories_RejectsSubdirectoryOfRoot(t *testing.T) {
	_, cfg, router := newDefaultDirTestHandler(t)

	w := putDefaultDir(t, router, filepath.Join(cfg.StacksDir, "sub"))
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}
