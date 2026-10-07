package handlers

import (
	"bytes"
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
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-z91e.3: every compose and env write under a stack directory is a
// temp-file-plus-rename, so a crash or ENOSPC mid-write can never leave a
// truncated file. A hardlink to the old inode is the observable difference:
// os.WriteFile truncates and rewrites that inode in place (the link sees the
// NEW bytes), a rename replaces the directory entry and leaves the old inode
// holding the OLD bytes.

// assertWrittenByRename saves the route's body through the fixture and checks
// that file now holds want while a hardlink taken beforehand still holds the
// old bytes, at the expected mode, with no temp file left behind.
func assertWrittenByRename(t *testing.T, method, route, body string, withEnv bool, file, want string, wantMode os.FileMode, okStatus int) {
	t.Helper()
	assertWrittenByRenameFrom(t, method, route, body, withEnv, file, want, wantMode, okStatus, nil)
}

// assertWrittenByRenameFrom is assertWrittenByRename with a hook that can
// reshape the fixture (e.g. chmod an existing file) before the request.
func assertWrittenByRenameFrom(t *testing.T, method, route, body string, withEnv bool, file, want string, wantMode os.FileMode, okStatus int, prepare func(stackDir string)) {
	t.Helper()
	router, _, stackID, stackDir := fileLockFixture(t, withEnv)
	if prepare != nil {
		prepare(stackDir)
	}
	path := filepath.Join(stackDir, file)

	var oldBytes []byte
	link := filepath.Join(t.TempDir(), "old-inode")
	if _, err := os.Stat(path); err == nil {
		//nolint:gosec // path is under the test's own TempDir
		oldBytes, err = os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.Link(path, link))
	}

	req := httptest.NewRequest(method, "/api/"+stackID+route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, okStatus, w.Code, "body: %s", w.Body.String())

	//nolint:gosec // path is under the test's own TempDir
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, string(got))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, wantMode, info.Mode().Perm(), "file mode")

	if oldBytes != nil {
		//nolint:gosec // link is under the test's own TempDir
		viaLink, err := os.ReadFile(link)
		require.NoError(t, err)
		assert.Equal(t, string(oldBytes), string(viaLink), "the old inode was rewritten in place (os.WriteFile) instead of replaced by a rename")
		linkInfo, err := os.Stat(link)
		require.NoError(t, err)
		assert.False(t, os.SameFile(info, linkInfo), "path still names the old inode")
	}

	entries, err := os.ReadDir(stackDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "temp file left behind")
	}
}

func TestComposePut_WritesByRename(t *testing.T) {
	assertWrittenByRename(t, http.MethodPut, "/compose",
		`{"content":"services:\n  web:\n    image: nginx\n"}`,
		false, "compose.yaml", "services:\n  web:\n    image: nginx\n", 0644, http.StatusOK)
}

func TestComposeAndEnvPut_ComposeWrittenByRename(t *testing.T) {
	assertWrittenByRename(t, http.MethodPut, "/compose-env",
		`{"composeContent":"services:\n  web:\n    image: nginx\n","envRaw":"B=2\n"}`,
		true, "compose.yaml", "services:\n  web:\n    image: nginx\n", 0644, http.StatusOK)
}

func TestComposeAndEnvPut_EnvWrittenByRename(t *testing.T) {
	assertWrittenByRename(t, http.MethodPut, "/compose-env",
		`{"composeContent":"services:\n  web:\n    image: nginx\n","envRaw":"B=2\n"}`,
		true, ".env", "B=2\n", 0600, http.StatusOK)
}

func TestEnvCreate_WritesEnvAt0600(t *testing.T) {
	assertWrittenByRename(t, http.MethodPost, "/env", `{"content":"B=2\n"}`,
		false, ".env", "B=2\n", 0600, http.StatusCreated)
}

// TestStacksCreate_WritesComposeAndEnvWithModes covers the two Create sites
// (a new directory, so there is no old inode to hold): modes are 0644 / 0600
// and no temp file survives.
func TestStacksCreate_WritesComposeAndEnvWithModes(t *testing.T) {
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))
	cfg := &config.Config{StacksDir: root}
	handler := NewStacksHandler(&fakeStackDocker{}, services.NewScannerService(cfg, db), services.NewLinterService(),
		db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	body, err := json.Marshal(map[string]any{
		"name":           "fresh",
		"composeContent": rollbackTestCompose,
		"envContent":     "A=1\n",
		"deploy":         false,
	})
	require.NoError(t, err)
	router := newCreateRouter(handler)
	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	dir := filepath.Join(root, "fresh")
	for name, mode := range map[string]os.FileMode{"compose.yaml": 0644, ".env": 0600} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err, name)
		assert.Equal(t, mode, info.Mode().Perm(), name)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "temp file left behind")
	}
}

func newCreateRouter(handler *StacksHandler) *gin.Engine {
	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	return router
}

// chmodStackFiles sets the fixture's existing files to mode, regardless of umask.
func chmodStackFiles(mode os.FileMode, names ...string) func(string) {
	return func(stackDir string) {
		for _, n := range names {
			if err := os.Chmod(filepath.Join(stackDir, n), mode); err != nil {
				panic(err)
			}
		}
	}
}

// An existing compose file keeps its permission bits across a save (Edwin's
// decision on agent-os-z91e.3: a host-edited 0664 file must stay 0664), and an
// existing env file is tightened to 0600, never kept looser.
func TestComposePut_KeepsExistingMode0664(t *testing.T) {
	assertWrittenByRenameFrom(t, http.MethodPut, "/compose",
		`{"content":"services:\n  web:\n    image: nginx\n"}`,
		false, "compose.yaml", "services:\n  web:\n    image: nginx\n", 0664, http.StatusOK,
		chmodStackFiles(0664, "compose.yaml"))
}

func TestComposeAndEnvPut_KeepsExistingComposeMode0664_EnvForced0600(t *testing.T) {
	prep := chmodStackFiles(0664, "compose.yaml", ".env")
	body := `{"composeContent":"services:\n  web:\n    image: nginx\n","envRaw":"B=2\n"}`
	assertWrittenByRenameFrom(t, http.MethodPut, "/compose-env", body, true,
		"compose.yaml", "services:\n  web:\n    image: nginx\n", 0664, http.StatusOK, prep)
	assertWrittenByRenameFrom(t, http.MethodPut, "/compose-env", body, true,
		".env", "B=2\n", 0600, http.StatusOK, prep)
}
