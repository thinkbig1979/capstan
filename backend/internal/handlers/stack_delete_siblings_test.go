package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

const deleteSiblingCompose = "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped\n"

// deleteSiblingFixture wires a real DB, scanner, linter and handler over a
// temporary stacks root, plus a router carrying Create and Delete. Everything
// below drives the real handlers; nothing about the removal path is faked.
type deleteSiblingFixture struct {
	tempDir string
	db      *database.DB
	scanner *services.ScannerService
	router  *gin.Engine
}

func newDeleteSiblingFixture(t *testing.T) *deleteSiblingFixture {
	t.Helper()

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(&fakeStackDocker{}, scanner, services.NewLinterService(), db, cfg,
		services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	return &deleteSiblingFixture{tempDir: tempDir, db: db, scanner: scanner, router: router}
}

// create posts a real Create request. envContent is written as .env when non-empty.
func (f *deleteSiblingFixture) create(t *testing.T, name, envContent string) {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"name":           name,
		"composeContent": deleteSiblingCompose,
		"envContent":     envContent,
		"deploy":         false,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "setup: Create %q must succeed, body=%s", name, w.Body.String())
}

// addSiblingStack drops an extra compose file into an existing stack directory
// and scans it — the ordinary way one directory comes to hold several stacks
// (stack IDs are root~path:project, so this is legal and supported).
func (f *deleteSiblingFixture) addSiblingStack(t *testing.T, stackDir, composeFile string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, composeFile), []byte(deleteSiblingCompose), 0o644))
	require.NoError(t, f.scanner.ScanDirectoryWithRoot(stackDir, f.tempDir))
}

// stackIDFor returns the registered stack ID whose compose file matches.
func (f *deleteSiblingFixture) stackIDFor(t *testing.T, stackDir, composeFile string) string {
	t.Helper()
	stacks, err := f.db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	for _, s := range stacks {
		if s.ComposeFile == composeFile {
			return s.ID
		}
	}
	t.Fatalf("no stack registered under %s with compose file %s (have %+v)", stackDir, composeFile, stacks)
	return ""
}

func (f *deleteSiblingFixture) delete(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true", nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// TestStacksHandler_Delete_PreservesSiblingComposeFiles is the regression guard
// for the data loss in agent-os-xa7: Delete used os.RemoveAll on the whole stack
// DIRECTORY, but one directory legitimately holds several stacks (one per
// compose file). Deleting one stack therefore destroyed every sibling stack's
// compose file while leaving the siblings' rows — and their containers running,
// since DeleteVerified composes down only the deleted stack's project.
func TestStacksHandler_Delete_PreservesSiblingComposeFiles(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "my-stack")

	f.create(t, "my-stack", "")
	f.addSiblingStack(t, stackDir, "compose.api.yaml")

	before, err := f.db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, before, 2, "setup: expected two stacks under %s", stackDir)

	defaultID := f.stackIDFor(t, stackDir, "compose.yaml")
	siblingID := f.stackIDFor(t, stackDir, "compose.api.yaml")

	w := f.delete(t, defaultID)
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed, body=%s", w.Body.String())

	// The sibling stack is untouched: its compose file and its row both survive.
	assert.FileExists(t, filepath.Join(stackDir, "compose.api.yaml"),
		"deleting one stack must not destroy a sibling stack's compose file")
	assert.DirExists(t, stackDir, "the directory must survive while another stack is registered under it")

	after, err := f.db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, after, 1, "only the deleted stack's row may go")
	assert.Equal(t, siblingID, after[0].ID)

	// The deleted stack really was deleted — otherwise "the sibling survived"
	// would be satisfied by a Delete that removes nothing at all.
	assert.NoFileExists(t, filepath.Join(stackDir, "compose.yaml"),
		"the deleted stack's own compose file must be removed")
}

// TestStacksHandler_Delete_LastStackRemovesDirectory is the positive control:
// deleting the only stack in a directory must still remove the directory from
// disk, exactly as before the fix.
func TestStacksHandler_Delete_LastStackRemovesDirectory(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "solo-stack")

	f.create(t, "solo-stack", "KEY=value\n")
	require.DirExists(t, stackDir)

	id := f.stackIDFor(t, stackDir, "compose.yaml")
	w := f.delete(t, id)
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed, body=%s", w.Body.String())

	assert.NoDirExists(t, stackDir, "deleting the last stack in a directory must remove the directory")

	remaining, err := f.db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

// TestStacksHandler_Delete_KeepsEnvFileSharedWithSibling covers the second way
// a delete can take a sibling's data with it: determineEnvFile (scanner.go)
// falls back to .env for a named stack that has no .env.<name>, so two stacks in
// one directory can legitimately reference the SAME env file. Removing the
// deleted stack's env file unconditionally would break the survivor.
func TestStacksHandler_Delete_KeepsEnvFileSharedWithSibling(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "my-stack")

	f.create(t, "my-stack", "SHARED=yes\n")
	f.addSiblingStack(t, stackDir, "compose.api.yaml")

	siblingID := f.stackIDFor(t, stackDir, "compose.api.yaml")
	sibling, err := f.db.GetStack(siblingID)
	require.NoError(t, err)
	require.Equal(t, ".env", sibling.EnvFile, "setup: the sibling must share the default .env")

	w := f.delete(t, f.stackIDFor(t, stackDir, "compose.yaml"))
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed, body=%s", w.Body.String())

	assert.FileExists(t, filepath.Join(stackDir, ".env"),
		"an env file still referenced by a surviving stack must not be removed")
}

// TestRemoveStackFile_RefusesPathsOutsideStackDir covers the new per-file
// removal surface: compose_file and env_file come from the database, so a
// malformed row must not be able to reach outside the stack's own directory.
func TestRemoveStackFile_RefusesPathsOutsideStackDir(t *testing.T) {
	stackDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "keep.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))

	for _, name := range []string{"../../etc/passwd", "..", "../sibling-stack/compose.yaml", "sub/../../escape"} {
		err := removeStackFile(stackDir, name)
		assert.Error(t, err, "removeStackFile(%q) must be refused", name)
		if err != nil {
			assert.Contains(t, err.Error(), "resolves outside the stack directory")
		}
	}

	// An absolute name is confined rather than refused: filepath.Join re-roots it
	// under the stack directory, so it can never reach the real path it names.
	assert.NoError(t, removeStackFile(stackDir, outside))
	assert.FileExists(t, outside, "an absolute env/compose value must not reach outside the stack directory")

	// An empty name and an absent file are both no-ops, not errors: a stack row
	// can carry no env file, and it can outlive the file it points at.
	assert.NoError(t, removeStackFile(stackDir, ""))
	assert.NoError(t, removeStackFile(stackDir, "compose.yaml"))
}

// TestStacksHandler_Delete_RemovesOwnEnvFile is the counterpart control: when
// the deleted stack owns its env file outright, that file must go, while the
// sibling's own files stay.
func TestStacksHandler_Delete_RemovesOwnEnvFile(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "my-stack")

	f.create(t, "my-stack", "DEFAULT=yes\n")
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env.api"), []byte("API=yes\n"), 0o600))
	f.addSiblingStack(t, stackDir, "compose.api.yaml")

	apiID := f.stackIDFor(t, stackDir, "compose.api.yaml")
	api, err := f.db.GetStack(apiID)
	require.NoError(t, err)
	require.Equal(t, ".env.api", api.EnvFile, "setup: the api stack must own .env.api")

	w := f.delete(t, apiID)
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed, body=%s", w.Body.String())

	assert.NoFileExists(t, filepath.Join(stackDir, ".env.api"), "the deleted stack's own env file must be removed")
	assert.NoFileExists(t, filepath.Join(stackDir, "compose.api.yaml"))
	assert.FileExists(t, filepath.Join(stackDir, "compose.yaml"), "the surviving stack's compose file must stay")
	assert.FileExists(t, filepath.Join(stackDir, ".env"), "the surviving stack's env file must stay")
}

// TestStacksHandler_Delete_LastStackRemovesDirectoryRow is the regression guard
// for agent-os-660: deleting the sole stack under a directory used to remove
// the directory from disk and its own stacks row, but left the directories row
// behind — orphaned, since nothing referenced it and the path no longer
// existed. ListDirectories (the directories API) surfaced that orphan forever,
// since nothing but a full Rescan (pruneStaleStacks, ScanAll-only) ever cleaned
// it up.
func TestStacksHandler_Delete_LastStackRemovesDirectoryRow(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "solo-stack")

	f.create(t, "solo-stack", "")
	_, err := f.db.GetDirectory(stackDir)
	require.NoError(t, err, "setup: Create must register a directories row")

	id := f.stackIDFor(t, stackDir, "compose.yaml")
	w := f.delete(t, id)
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed, body=%s", w.Body.String())

	_, err = f.db.GetDirectory(stackDir)
	assert.Error(t, err, "the directories row for a directory with no remaining stacks must be removed")
	assert.True(t, errors.Is(err, sql.ErrNoRows), "expected sql.ErrNoRows, got %v", err)
}

// TestStacksHandler_Delete_SiblingPresent_DirectoryRowSurvives guards the other
// side of agent-os-660: an unconditional directories-row delete on every Delete
// would reintroduce the agent-os-w8o cascade from the other end, since
// stacks.directory has ON DELETE CASCADE onto directories.path — removing the
// row for a directory that still holds a sibling stack would take that
// sibling's row down with it. The directories row must survive as long as any
// stack remains registered under it.
func TestStacksHandler_Delete_SiblingPresent_DirectoryRowSurvives(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "my-stack")

	f.create(t, "my-stack", "")
	f.addSiblingStack(t, stackDir, "compose.api.yaml")

	siblingID := f.stackIDFor(t, stackDir, "compose.api.yaml")

	w := f.delete(t, f.stackIDFor(t, stackDir, "compose.yaml"))
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed, body=%s", w.Body.String())

	_, err := f.db.GetDirectory(stackDir)
	assert.NoError(t, err, "the directories row must survive while a sibling stack is still registered under it")

	// The sibling's own row must be untouched too — not just present, but
	// still resolvable to the same stack ID (a cascade would have taken it
	// down along with the directories row).
	sibling, err := f.db.GetStack(siblingID)
	require.NoError(t, err)
	assert.Equal(t, siblingID, sibling.ID)
}
