package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Characterisation, not fail-first: Create was already safe against a symlink
// at the new stack's own path, and these pin why (agent-os-qags.2). The name is
// one path component, so the only place a link can sit is stackDir itself.
func postCreate(t *testing.T, f *deleteSiblingFixture, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":           name,
		"composeContent": deleteSiblingCompose,
		"envContent":     "SECRET=1\n",
		"deploy":         false,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// A dangling link at root/<name>: os.Stat reports it absent, so the duplicate
// check passes, and os.MkdirAll then fails on it rather than following it.
func TestStacksHandler_Create_DanglingSymlinkAtStackDirWritesNothingOutside(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	outside := filepath.Join(t.TempDir(), "not-yet")
	require.NoError(t, os.Symlink(outside, filepath.Join(f.tempDir, "evil")))

	w := postCreate(t, f, "evil")

	assert.GreaterOrEqual(t, w.Code, 400, "body=%s", w.Body.String())
	_, err := os.Lstat(outside)
	assert.True(t, os.IsNotExist(err), "nothing may be created outside the root, Lstat err=%v", err)
}

// A link at root/<name> to an existing directory outside the root: os.Stat
// follows it, sees a directory, and Create answers 409 before writing.
func TestStacksHandler_Create_SymlinkToOutsideDirAtStackDirIsConflict(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(f.tempDir, "evil")))

	w := postCreate(t, f, "evil")

	assert.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.NoFileExists(t, filepath.Join(outside, "compose.yaml"))
	assert.NoFileExists(t, filepath.Join(outside, ".env"))
}
