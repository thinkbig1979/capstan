package handlers

import (
	"bytes"
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

const rollbackTestCompose = "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped\n"

// upsertFailingStore is the real *database.DB with UpsertStack replaced by a
// hard error. Create's directory-registration rollback is only reachable when
// UpsertStack fails, and that failure is a DB fault (locked/closed/IO), not
// anything a request body can express — so injecting it at the stackStore seam
// is the only way to exercise the branch. Everything else in the request runs
// unmodified against the real database.
type upsertFailingStore struct {
	*database.DB
	err error
}

func (s *upsertFailingStore) UpsertStack(models.Stack) error { return s.err }

// createStackRequest posts a Create for name and returns the recorder.
func createStackRequest(t *testing.T, handler *StacksHandler, name string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	body, err := json.Marshal(map[string]any{
		"name":           name,
		"composeContent": rollbackTestCompose,
		"deploy":         false,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestStacksHandler_Create_RollbackDoesNotCascadeSiblingStacks proves the
// rollback at stack_crud.go:206 is non-destructive when the directories row it
// unregisters was NOT created by this request.
//
// Create's duplicate guard is a filesystem check (os.Stat on stackDir) but the
// rollback it guards is a DATABASE cascade: stacks.directory has an FK to
// directories(path) ON DELETE CASCADE, so deleting the directories row deletes
// every stack row whose directory equals that path. The two can disagree, and
// the Delete handler is what makes them disagree in ordinary operation: it
// os.RemoveAll's the directory and deletes its own stacks row, but never
// unregisters the directory. That leaves a directories row whose directory is
// gone from disk, so the next Create for the same path sails past the 409 and
// re-arms the cascade against any sibling stack still registered there.
//
// The whole sequence below runs through real handler calls; only UpsertStack's
// failure is injected, because the rollback branch has no other trigger.
func TestStacksHandler_Create_RollbackDoesNotCascadeSiblingStacks(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	scanner := services.NewScannerService(cfg, db)
	actionLog := services.NewActionLogger(db)
	opLock := services.NewOperationLock()
	stackDir := filepath.Join(tempDir, "my-stack")

	// 1. Create the stack for real: directory on disk, directories row, stacks row.
	realHandler := NewStacksHandler(&fakeStackDocker{}, scanner, linter, db, cfg, actionLog, opLock)
	w := createStackRequest(t, realHandler, "my-stack")
	require.Equal(t, http.StatusCreated, w.Code, "setup: first Create must succeed, body=%s", w.Body.String())
	require.DirExists(t, stackDir)

	// 2. The operator adds a second compose file to that directory and it gets
	//    scanned — the ordinary way one directory comes to hold several stacks
	//    (IDs are root~path:project, so this is legal and supported).
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.api.yaml"), []byte(rollbackTestCompose), 0o644))
	require.NoError(t, scanner.ScanDirectoryWithRoot(stackDir, tempDir))

	registered, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, registered, 2, "setup: expected two stacks under %s", stackDir)

	var defaultID, siblingID string
	for _, s := range registered {
		if s.ComposeFile == "compose.yaml" {
			defaultID = s.ID
		} else {
			siblingID = s.ID
		}
	}
	require.NotEmpty(t, defaultID)
	require.NotEmpty(t, siblingID)

	// 3. Delete the default stack through the real handler. It removes the whole
	//    directory from disk and its own stacks row, but leaves the directories
	//    row behind — DeleteStack touches only the stacks table and the handler
	//    never calls UnregisterDirectory.
	delRouter := gin.New()
	delRouter.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), realHandler.Delete)
	delReq := httptest.NewRequest(http.MethodDelete, "/stacks/"+defaultID+"?confirm=true", nil)
	delW := httptest.NewRecorder()
	delRouter.ServeHTTP(delW, delReq)
	require.Equal(t, http.StatusOK, delW.Code, "setup: Delete must succeed, body=%s", delW.Body.String())

	// The disagreement now exists: directory gone from disk, directories row live.
	_, statErr := os.Stat(stackDir)
	require.True(t, os.IsNotExist(statErr), "setup: Delete must remove %s from disk", stackDir)
	staleDir, err := db.GetDirectory(stackDir)
	require.NoError(t, err, "setup: the directories row must outlive its directory")
	require.Equal(t, stackDir, staleDir.Path)

	survivors, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, survivors, 1, "setup: the sibling stack must still be registered")
	require.Equal(t, siblingID, survivors[0].ID)

	// 4. Re-create the same stack. os.Stat finds nothing, so the 409 guard does
	//    not fire; RegisterDirectory upserts the row that was already there; then
	//    UpsertStack fails and the rollback runs against a row it did not create.
	failing := &upsertFailingStore{DB: db, err: errors.New("database is locked")}
	failingHandler := NewStacksHandler(&fakeStackDocker{}, scanner, linter, failing, cfg, actionLog, opLock)
	w2 := createStackRequest(t, failingHandler, "my-stack")
	require.Equal(t, http.StatusInternalServerError, w2.Code,
		"the injected UpsertStack failure must reach the rollback branch, body=%s", w2.Body.String())

	after, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	t.Logf("OBSERVED sibling stacks under %s: before=%d after=%d", stackDir, len(survivors), len(after))

	assert.Len(t, after, 1,
		"rolling back one failed Create must not cascade away the sibling stack registered under the same directory")
	if len(after) == 1 {
		assert.Equal(t, siblingID, after[0].ID)
	}

	// The pre-existing directories row is not this request's to remove either.
	_, err = db.GetDirectory(stackDir)
	assert.NoError(t, err, "rollback must leave a directories row it did not create")
}

// TestStacksHandler_Create_RollbackUnregistersOwnDirectory is the counterpart
// guard: when Create really did insert the directories row, a failed UpsertStack
// must still roll that registration back, so a failed create never leaves an
// orphan directories row behind. Without this, "no rows deleted" above could be
// satisfied by a rollback that simply stopped running.
func TestStacksHandler_Create_RollbackUnregistersOwnDirectory(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	failing := &upsertFailingStore{DB: db, err: errors.New("database is locked")}
	handler := NewStacksHandler(&fakeStackDocker{}, services.NewScannerService(cfg, db), services.NewLinterService(),
		failing, cfg, services.NewActionLogger(db), services.NewOperationLock())

	stackDir := filepath.Join(tempDir, "fresh-stack")
	w := createStackRequest(t, handler, "fresh-stack")
	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())

	_, err = db.GetDirectory(stackDir)
	assert.Error(t, err, "a failed Create that registered the directory itself must unregister it")
	assert.NoDirExists(t, stackDir, "a failed Create must remove the directory it created")
}

// TestStacksHandler_Create_PositiveControl_FreshDirSucceeds keeps the two tests
// above honest: an ordinary Create against a directory that does not exist must
// still return 201. Without it, "nothing was deleted" could equally mean Create
// stopped working.
func TestStacksHandler_Create_PositiveControl_FreshDirSucceeds(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	handler := NewStacksHandler(&fakeStackDocker{}, services.NewScannerService(cfg, db), services.NewLinterService(),
		db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	w := createStackRequest(t, handler, "my-stack")
	t.Logf("OBSERVED control status=%d body=%s", w.Code, w.Body.String())
	require.Equal(t, http.StatusCreated, w.Code)

	stacks, err := db.ListStacksByDirectory(filepath.Join(tempDir, "my-stack"))
	require.NoError(t, err)
	assert.Len(t, stacks, 1)
}
