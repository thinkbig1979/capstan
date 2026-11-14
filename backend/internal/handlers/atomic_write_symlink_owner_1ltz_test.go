package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// agent-os-1ltz: an env save through a symlink replaced the TARGET with a new
// file at 0600 owned by the Capstan process, which could lock a host user out
// of a shared env file. Edwin's ruling (2026-11-14): through a link inside the
// stacks root, the target keeps its mode and owner. A link outside the root is
// refused before any write (400 PATH_TRAVERSAL, pinned by the dwmz tests).
// A plain .env keeps the 0600 policy.
//
// Ownership is tested through a SEAM (processOwner, chownTempFile): as an
// unprivileged user the test cannot create a file owned by someone else, so
// it makes the target look foreign instead and records the chown. One arm
// does a real chown and runs only as root.

func ownerOf(t *testing.T, path string) (uid, gid int) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	st := info.Sys().(*syscall.Stat_t)
	return int(st.Uid), int(st.Gid)
}

// linkedEnvFixture is a stack whose .env is a link to ../shared/app.env inside
// the stacks root; it returns the router, stack id, unlock token and target.
func linkedEnvFixture(t *testing.T, mode os.FileMode) (http.Handler, string, string, string) {
	t.Helper()
	router, stackID, token, envPath := envQuoteFixture(t, "A=1\n")
	shared := filepath.Join(filepath.Dir(filepath.Dir(envPath)), "shared")
	require.NoError(t, os.MkdirAll(shared, 0o755))
	target := filepath.Join(shared, "app.env")
	require.NoError(t, os.WriteFile(target, []byte("A=1\n"), mode))
	require.NoError(t, os.Chmod(target, mode)) //nolint:gosec // test fixture: the target's mode is under test
	require.NoError(t, os.Remove(envPath))
	require.NoError(t, os.Symlink("../shared/app.env", envPath))
	return router, stackID, token, target
}

func putEntriesRaw(t *testing.T, router http.Handler, stackID, token, raw string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"raw": raw})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stackID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestEnvSave_1ltz_LinkKeepsTargetMode(t *testing.T) {
	router, stackID, token, target := linkedEnvFixture(t, 0o644)

	w := putEntriesRaw(t, router, stackID, token, "A=2\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assert.Equal(t, "A=2\n", readString(t, target))
	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "the link's target keeps its mode")
}

// A plain file keeps the existing policy: tightened to 0600.
func TestEnvSave_1ltz_PlainFileStillTightened(t *testing.T) {
	router, stackID, token, envPath := envQuoteFixture(t, "A=1\n")
	require.NoError(t, os.Chmod(envPath, 0o644)) //nolint:gosec // test fixture: a deliberately loose mode
	w := putEntriesRaw(t, router, stackID, token, "A=2\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	info, err := os.Stat(envPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
