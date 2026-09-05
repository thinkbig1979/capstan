package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

func TestStacksHandler_Create_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(nil, scanner, linter, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	user := models.User{
		ID:        "test-user-id",
		Username:  "testuser",
		Password:  "",
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}
	err = db.CreateUser(user)
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "my-stack")
	createTestDirectory(t, db, stackDir)

	reqBody := map[string]interface{}{
		"name":           "my-stack",
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped",
		"envContent":     "PORT=8080",
		"deploy":         false,
	}
	reqBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Create now returns a truth.ActionResult wire format:
	// {"outcome":"success","reason":"...","details":{"stack":{...},...}}
	assert.Equal(t, "success", response["outcome"])
	details := response["details"].(map[string]interface{})
	stack := details["stack"].(map[string]interface{})
	assert.Equal(t, filepath.Base(tempDir)+"~my-stack:default", stack["id"])

	assert.FileExists(t, filepath.Join(tempDir, "my-stack", "compose.yaml"))
	assert.FileExists(t, filepath.Join(tempDir, "my-stack", ".env"))
}

// TestStacksHandler_Create_UnindexedDirectory_FKEnforced is the regression
// test for agent-os-jcu: POST /api/v1/stacks 500'd for every stack directory
// the scanner had never indexed, because UpsertStack ran before any
// directories row existed for stackDir, and stacks.directory has an FK to
// directories(path) (migrations.go) enforced pool-wide for every connection
// database.NewWithMigrations opens (database.go's DSN _pragma=foreign_keys(1)).
//
// Unlike TestStacksHandler_Create_Success, this test deliberately does NOT
// call createTestDirectory for the new stack's directory first — that helper
// call is exactly what was masking the bug in the existing success test, by
// pre-satisfying the FK the handler itself was supposed to satisfy. This test
// reproduces the UI's actual common-case Create flow: a new subdirectory
// under an already-monitored root, which the scanner has never seen.
func TestStacksHandler_Create_UnindexedDirectory_FKEnforced(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	// Precondition: foreign_keys enforcement must be genuinely active on this
	// exact *database.DB, or this test would pass for the wrong reason. Probe
	// it directly (handlers can't reach the private db.db.QueryRow("PRAGMA
	// foreign_keys") migrations_test.go/pragma_test.go use from inside the
	// database package) by inserting a stack row against a directory that has
	// no row: that must fail with an FK violation if enforcement is on.
	probeErr := db.UpsertStack(models.Stack{
		ID: "fkprobe~no-such-dir:default", Directory: filepath.Join(tempDir, "does-not-exist"),
		ComposeFile: "compose.yaml", ProjectName: "fkprobe",
	})
	require.Error(t, probeErr, "test precondition: foreign_keys must be ON (insert against a nonexistent directory must fail)")
	require.NoError(t, db.DeleteStack("fkprobe~no-such-dir:default")) // no-op if the insert above didn't persist; keeps state clean either way

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(nil, scanner, linter, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	user := models.User{
		ID:        "test-user-id",
		Username:  "testuser",
		Password:  "",
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}
	require.NoError(t, db.CreateUser(user))

	// Deliberately NOT calling createTestDirectory(t, db, stackDir): the
	// directory for "new-stack" has never been scanned or otherwise
	// registered, which is exactly the state a brand-new stack directory is
	// in when a real user creates it through the UI.
	reqBody := map[string]interface{}{
		"name":           "new-stack",
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped",
		"envContent":     "PORT=8080",
		"deploy":         false,
	}
	reqBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "response body: %s", w.Body.String())

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "success", response["outcome"])

	expectedID := filepath.Base(tempDir) + "~new-stack:default"
	details := response["details"].(map[string]interface{})
	stack := details["stack"].(map[string]interface{})
	assert.Equal(t, expectedID, stack["id"])

	// Both rows must exist: the directory (satisfying the FK) and the stack.
	stackDir := filepath.Join(tempDir, "new-stack")
	dir, err := db.GetDirectory(stackDir)
	require.NoError(t, err)
	require.NotNil(t, dir)
	assert.Equal(t, stackDir, dir.Path)

	persisted, err := db.GetStack(expectedID)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, stackDir, persisted.Directory)
}

func TestStacksHandler_Create_ValidationError(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	handler := NewStacksHandler(nil, nil, linter, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", handler.Create)

	reqBody := map[string]interface{}{
		"name":           "my-stack",
		"composeContent": "services:\n  web:\n    restart: unless-stopped",
		"deploy":         false,
	}
	reqBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "COMPOSE_VALIDATION_ERROR", response["code"])
}

func TestStacksHandler_List_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	createTestDirectory(t, db, "/tmp/test/stack1")

	stack := models.Stack{
		ID:          "test~stack1:default",
		Directory:   "/tmp/test/stack1",
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
		Status:      "running",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/stacks", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/stacks", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	stacks := response["stacks"].([]interface{})
	assert.Len(t, stacks, 1)
}

// TestComposeUnreadable covers the error-vs-stopped decision for a stack with no
// live containers: a readable compose file means the stack is simply down
// ("stopped"), a missing/unreadable one means Capstan can't resolve it ("error" —
// what the old `docker compose ps` surfaced as "unknown").
func TestComposeUnreadable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644))

	readable := models.Stack{Directory: dir, ComposeFile: "compose.yaml"}
	assert.False(t, composeUnreadable(readable), "present compose file -> stopped, not error")

	missingFile := models.Stack{Directory: dir, ComposeFile: "nope.yaml"}
	assert.True(t, composeUnreadable(missingFile), "missing compose file -> error")

	missingDir := models.Stack{Directory: filepath.Join(dir, "gone"), ComposeFile: "compose.yaml"}
	assert.True(t, composeUnreadable(missingDir), "unreadable/missing dir -> error")
}

// TestApplyLiveStatus_GetMatchesList pins the shared snapshot-resolution logic
// that both List and Get use, so a stack's detail page agrees with its row in the
// list. A project present in the snapshot takes the live status + containers; a
// container-less project resolves to "stopped" (readable compose) or "error"
// (unreadable compose) -- never the old `docker compose ps` "unknown".
func TestApplyLiveStatus_GetMatchesList(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644))

	// Project present in snapshot -> live status + reconstructed containers.
	live := map[string]services.LiveStatus{
		"proj-default": {
			Status:     "running",
			Containers: []models.Container{{ID: "abc", Name: "web", Image: "nginx", State: "running", Status: "Up 2 hours", Health: "healthy"}},
		},
	}
	present := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "proj-default", Status: "stale"}
	applyLiveStatus(&present, live)
	assert.Equal(t, "running", present.Status)
	require.Len(t, present.Containers, 1)
	assert.Equal(t, "web", present.Containers[0].Name)
	assert.Equal(t, "healthy", present.Containers[0].Health)

	// No live containers, readable compose -> stopped. Containers is an EMPTY
	// slice, not nil (agent-os-kqp6): a nil slice serialises as
	// "containers":null, and stacks.go is the last of four such wire-shape
	// sites to standardise on "containers":[] (see
	// TestStacksHandler_Get_ContainerlessStackEmitsEmptyArray for the
	// raw-bytes proof this assertion can't provide by itself, since
	// reflect.DeepEqual — which assert.Equal on a slice ultimately uses — does
	// distinguish nil from empty, but a decoded JSON round-trip would not).
	stopped := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "proj-default", Status: "running"}
	applyLiveStatus(&stopped, map[string]services.LiveStatus{})
	assert.Equal(t, "stopped", stopped.Status)
	assert.Equal(t, []models.Container{}, stopped.Containers)

	// No live containers, unreadable compose -> error (the old Get returned "unknown").
	broken := models.Stack{Directory: dir, ComposeFile: "missing.yaml", ProjectName: "proj-default", Status: "running"}
	applyLiveStatus(&broken, map[string]services.LiveStatus{})
	assert.Equal(t, "error", broken.Status)
	assert.Equal(t, []models.Container{}, broken.Containers)
}

func TestStacksHandler_Get_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	createTestDirectory(t, db, "/tmp/test/stack1")

	stack := models.Stack{
		ID:          "test~stack1:default",
		Directory:   "/tmp/test/stack1",
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
		Status:      "running",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/stacks/:id", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/test~stack1:default", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "test~stack1:default", response["id"])
}

func TestStacksHandler_Get_NotFound(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.GET("/stacks/:id", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/test~nonexistent:default", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "STACK_NOT_FOUND", response["code"])
}

// TestStacksHandler_Delete_OutsideRootRefused proves the path-traversal guard in
// Delete fires at the HANDLER level: a stack whose Directory points outside the
// configured stacks root must be refused with a "failed" outcome BEFORE any
// removal, and the directory must be left untouched. docker is nil here because
// the guard returns before h.docker.DeleteVerified is ever reached — this is exactly the
// property under test (a regression reordering the guard would panic or delete).
func TestStacksHandler_Delete_OutsideRootRefused(t *testing.T) {
	tempRoot := t.TempDir() // configured stacks root
	outside := t.TempDir()  // a DIFFERENT root — not inside tempRoot
	marker := filepath.Join(outside, "keep.txt")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempRoot} // GetAllStacksDirs() must NOT include `outside`
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	// Insert a stack row whose Directory points OUTSIDE the configured root.
	createTestDirectory(t, db, outside)
	stackID := "outside~evil:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          stackID,
		Directory:   outside,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "evil-default",
		Status:      "running",
	}))

	router := gin.New()
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+stackID+"?confirm=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "failed", resp["outcome"])
	assert.Contains(t, resp["reason"], "outside the configured stacks root")

	// The guard fired before os.RemoveAll: the directory and its contents survive.
	assert.FileExists(t, marker)
	assert.DirExists(t, outside)
}

// fakeStackDocker is a stackDocker test double. The zero value reports docker as
// present and lets DeleteVerified succeed, so a Delete request proceeds past the
// real compose-down to the directory removal and DB delete.
type fakeStackDocker struct {
	deleteAR     truth.ActionResult
	deleteOutput string
}

func (f *fakeStackDocker) GetStackStatuses(context.Context, services.DashboardDB) (map[string]services.LiveStatus, error) {
	return map[string]services.LiveStatus{}, nil
}
func (f *fakeStackDocker) StartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *fakeStackDocker) StopVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *fakeStackDocker) RestartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *fakeStackDocker) PullVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *fakeStackDocker) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	if f.deleteAR.Outcome == "" {
		return truth.Success("stack and volumes removed"), f.deleteOutput
	}
	return f.deleteAR, f.deleteOutput
}

// fakeStackStore is a stackStore test double: GetStack returns a preconfigured
// stack so the handler proceeds, and DeleteStack returns deleteErr so the
// db-delete error branch can be driven without a real database.
type fakeStackStore struct {
	stack        *models.Stack
	deleteErr    error
	deleteCalled bool
}

func (f *fakeStackStore) ListStacks() ([]models.Stack, error)                 { return nil, nil }
func (f *fakeStackStore) GetStack(string) (*models.Stack, error)              { return f.stack, nil }
func (f *fakeStackStore) GetStackByProjectName(string) (*models.Stack, error) { return nil, nil }

// ListStacksByDirectory is inert for every fixture in this file: it can only
// return either an empty slice or a slice containing solely the stack under
// test, and Delete's survivor computation (listSurvivors, stack_crud.go)
// filters that same stack out by ID either way, leaving zero survivors on
// both paths. Mutating this to `return nil, nil` unconditionally still
// leaves the package green — verified 2026-08-03 against
// TestStacksHandler_Delete_DBDeleteErrorSurfaced and the rest of this file.
// It exists only so GetStack's preconfigured stack has a directory listing
// to answer with; it cannot express a sibling scenario. Tests that need
// Delete's sibling/collateral/race behaviour use a real *database.DB instead
// — see stack_delete_siblings_test.go, stack_delete_collateral_test.go, and
// stack_delete_race_test.go.
func (f *fakeStackStore) ListStacksByDirectory(path string) ([]models.Stack, error) {
	if f.stack == nil || f.stack.Directory != path {
		return nil, nil
	}
	return []models.Stack{*f.stack}, nil
}

func (f *fakeStackStore) UpsertStack(models.Stack) error         { return nil }
func (f *fakeStackStore) UpdateStackStatus(string, string) error { return nil }
func (f *fakeStackStore) DeleteStack(string) error {
	f.deleteCalled = true
	return f.deleteErr
}

// DeleteDirectoryIfOrphaned is a no-op success: none of fakeStackStore's
// current callers assert on directory-row cleanup, and the handler treats
// this call as best-effort regardless.
func (f *fakeStackStore) DeleteDirectoryIfOrphaned(string) (bool, error) { return false, nil }

// TestStacksHandler_Delete_DBDeleteErrorSurfaced proves the handler surfaces a
// db.DeleteStack failure that occurs AFTER the stack is brought down and its
// directory removed, rather than reporting a false success. Docker and the store
// are faked via the consumer-side stackDocker/stackStore interfaces; the guard,
// path resolution and real os.RemoveAll all run unmodified.
func TestStacksHandler_Delete_DBDeleteErrorSurfaced(t *testing.T) {
	tempRoot := t.TempDir()
	stackDir := filepath.Join(tempRoot, "mystack") // inside the configured root -> guard passes
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {}\n"), 0o644))

	id := "root~mystack:default"
	store := &fakeStackStore{
		stack:     &models.Stack{ID: id, Directory: stackDir, ComposeFile: "compose.yaml", ProjectName: "mystack-default"},
		deleteErr: errors.New("database is locked"),
	}
	docker := &fakeStackDocker{deleteOutput: "Removing network mystack_default"}

	// A real ActionLogger over an in-memory DB: logAction writes there and swallows
	// errors, so it never interferes with the persistence path under test.
	logDB, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	cfg := &config.Config{StacksDir: tempRoot}
	handler := NewStacksHandler(docker, nil, nil, store, cfg, services.NewActionLogger(logDB), services.NewOperationLock())

	router := gin.New()
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "failed", resp["outcome"])
	assert.Contains(t, resp["reason"], "DB delete failed")
	assert.True(t, store.deleteCalled, "handler must reach db.DeleteStack")

	// The handler progressed through the real os.RemoveAll before the DB delete
	// failed: the directory is gone even though the row deletion errored.
	assert.NoDirExists(t, stackDir)
}

// TestStacksHandler_Delete_VerificationFailureLeavesDirAndDBIntact proves the
// accepted behavior change: when DeleteVerified reports a Failed outcome (e.g.
// compose down exited 0 but a container survived), the handler must NOT touch
// the filesystem or the DB row — this is what prevents orphaning a surviving
// container by deleting its stack directory out from under it.
func TestStacksHandler_Delete_VerificationFailureLeavesDirAndDBIntact(t *testing.T) {
	tempRoot := t.TempDir()
	stackDir := filepath.Join(tempRoot, "mystack") // inside the configured root -> guard passes
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {}\n"), 0o644))

	id := "root~mystack:default"
	store := &fakeStackStore{
		stack: &models.Stack{ID: id, Directory: stackDir, ComposeFile: "compose.yaml", ProjectName: "mystack-default"},
	}
	docker := &fakeStackDocker{
		deleteAR:     truth.Failed("stack not fully removed: 1 container(s) still present (paused:1)", nil),
		deleteOutput: "Removing network mystack_default",
	}

	logDB, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	cfg := &config.Config{StacksDir: tempRoot}
	handler := NewStacksHandler(docker, nil, nil, store, cfg, services.NewActionLogger(logDB), services.NewOperationLock())

	router := gin.New()
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "failed", resp["outcome"])
	assert.Contains(t, resp["reason"], "compose down did not verify as removed")
	assert.Contains(t, resp["reason"], "still present")

	// The handler must have returned before os.RemoveAll and before DeleteStack.
	assert.DirExists(t, stackDir, "verification failure must leave the stack directory on disk")
	assert.False(t, store.deleteCalled, "verification failure must NOT reach db.DeleteStack")
}

// Two configured roots that share a basename must not mint the same stack ID.
// Before the fix both Creates produced "stacks~my-stack:default" and
// UpsertStack's INSERT OR REPLACE silently repointed the first row at the
// second directory, leaving the first stack unreachable by ID (agent-os-elo).
func TestStacksHandler_Create_SameBasenameRootsGetDistinctIDs(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a", "stacks")
	rootB := filepath.Join(base, "b", "stacks")
	require.NoError(t, os.MkdirAll(rootA, 0755))
	require.NoError(t, os.MkdirAll(rootB, 0755))

	cfg := &config.Config{StacksDir: rootA, ExtraStacksDirs: []string{rootB}}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(nil, scanner, services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	create := func(dir, compose string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{
			"name": "my-stack", "directory": dir, "composeContent": compose, "deploy": false,
		})
		req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	wa := create(rootA, "services:\n  a:\n    image: nginx:1.21\n    restart: unless-stopped")
	require.Equal(t, http.StatusCreated, wa.Code, "create under root A: %s", wa.Body.String())
	wb := create(rootB, "services:\n  b:\n    image: redis:7\n    restart: unless-stopped")
	require.Equal(t, http.StatusCreated, wb.Code, "create under root B: %s", wb.Body.String())

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 2, "both stacks must survive; a collision collapses them into one row")

	byDir := map[string]string{}
	for _, s := range stacks {
		byDir[s.Directory] = s.ID
	}
	assert.Contains(t, byDir, filepath.Join(rootA, "my-stack"))
	assert.Contains(t, byDir, filepath.Join(rootB, "my-stack"))
	assert.NotEqual(t, byDir[filepath.Join(rootA, "my-stack")], byDir[filepath.Join(rootB, "my-stack")])

	// rootA is the PRIMARY root, so it is the extra root that moves. Configuring
	// a colliding extra root must never re-ID a stack under the primary one.
	assert.Equal(t, "stacks~my-stack:default", byDir[filepath.Join(rootA, "my-stack")])
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~my-stack:default$`, byDir[filepath.Join(rootB, "my-stack")])
}

// Load-bearing guard: for a single configured root the ID Create mints is
// byte-for-byte what it has always been. The agent-os-elo disambiguator is
// collision-only; silently re-IDing every existing stack would orphan six
// unenforced TEXT columns plus every bookmarked frontend URL.
func TestStacksHandler_Create_SingleRootIDIsUnchanged(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "srv", "stacks")
	require.NoError(t, os.MkdirAll(root, 0755))

	cfg := &config.Config{StacksDir: root}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(nil, scanner, services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	body, _ := json.Marshal(map[string]interface{}{
		"name":           "my-stack",
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped",
		"deploy":         false,
	})
	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "create: %s", w.Body.String())

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Equal(t, "stacks~my-stack:default", stacks[0].ID)
}

// A disambiguated ID travels in the :id path segment of every stack route, so
// it has to survive the request validator. stackIDRegex permits "." — this test
// is what fails if the agent-os-elo joiner is ever changed to a character the
// validator rejects, which would 400 every request for a stack under a
// same-basename root.
func TestStackID_DisambiguatedIDPassesRouteValidation(t *testing.T) {
	extras := []string{"/opt/stacks"}

	id := services.StackID(extras[0], "/srv/stacks", extras, "my-stack", "default")

	require.NotEqual(t, "stacks~my-stack:default", id, "guard is only meaningful for a disambiguated ID")
	assert.True(t, middleware.ValidateStackID(id), "disambiguated stack ID %q is rejected by ValidateStackID", id)
}

// TestStacksHandler_Create_MkdirFailureLogsCause is the SEEN-FAILING-FIRST test
// for agent-os-ua4y at stack_crud.go's MKDIR_ERROR site (StacksHandler.Create):
// before the fix this wrote c.JSON(500, ...) directly and logged nothing, so an
// operator saw a 500 with no record of why. cfg.StacksDir is pointed at a
// regular FILE rather than a directory, so os.MkdirAll(stackDir, ...) fails
// deterministically with a real OS error — no fake needed for this one.
// CONTROL: response status and code are unchanged by routing through
// handleError instead of c.JSON directly.
func TestStacksHandler_Create_MkdirFailureLogsCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	tempDir := t.TempDir()
	blockingFile := filepath.Join(tempDir, "not-a-directory")
	require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: blockingFile}
	linter := services.NewLinterService()
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(nil, scanner, linter, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	user := models.User{ID: "test-user-id", Username: "testuser", Password: "", CreatedAt: testTime, UpdatedAt: testTime}
	require.NoError(t, db.CreateUser(user))

	reqBody := map[string]interface{}{
		"name":           "my-stack",
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped",
	}
	reqBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (routing must be unchanged by the fix)", w.Code)
	}
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	if resp["code"] != "MKDIR_ERROR" {
		t.Fatalf("code = %v, want MKDIR_ERROR (routing must be unchanged by the fix)", resp["code"])
	}
	if !strings.Contains(buf.String(), "not a directory") {
		t.Fatalf("500 emitted with no log of the underlying MkdirAll cause. captured = %q", buf.String())
	}
}

// TestStacksHandler_Get_DBFaultLogsCause is the SEEN-FAILING-FIRST test for
// agent-os-7lg1 at stacks.go's db.GetStack site (StacksHandler.Get): before the
// fix, ANY GetStack error — a genuine database fault, not just a missing row —
// was mapped to the same silent 404, discarding the real error. faultyDB(t)
// (faulty_db_test.go, agent-os-2mhb) fails with "sql: database is closed",
// which is NOT sql.ErrNoRows (proven by
// TestFaultyDB_FailsDifferentlyFromHealthyNotFound).
func TestStacksHandler_Get_DBFaultLogsCause(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("db fault surfaces as 500 with the cause logged", func(t *testing.T) {
		buf := captureHandlerLogs(t)
		broken := faultyDB(t)
		cfg := &config.Config{StacksDir: "/tmp/test"}
		handler := NewStacksHandler(nil, nil, nil, broken, cfg, services.NewActionLogger(broken), services.NewOperationLock())

		router := gin.New()
		router.GET("/stacks/:id", handler.Get)

		req := httptest.NewRequest(http.MethodGet, "/stacks/whatever", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (a real DB fault must not present as 404)", w.Code)
		}
		if !strings.Contains(buf.String(), "database is closed") {
			t.Fatalf("500 emitted with no log of the underlying db fault. captured = %q", buf.String())
		}
	})

	// CONTROL, same instrument: a healthy DB with a genuinely missing stack
	// must keep its exact existing 404 and emit no ERROR line.
	t.Run("control: healthy DB, missing stack, stays a silent 404 with no ERROR log", func(t *testing.T) {
		buf := captureHandlerLogs(t)
		db, err := database.NewWithMigrations(":memory:")
		require.NoError(t, err)
		cfg := &config.Config{StacksDir: "/tmp/test"}
		handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewActionLogger(db), services.NewOperationLock())

		router := gin.New()
		router.GET("/stacks/:id", handler.Get)

		req := httptest.NewRequest(http.MethodGet, "/stacks/whatever", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (missing row must keep its existing shape)", w.Code)
		}
		if strings.Contains(buf.String(), "ERROR") {
			t.Fatalf("a genuine not-found logged an ERROR line; it must stay silent. captured = %q", buf.String())
		}
	})
}

// statusFakeDocker is a minimal stackDocker test double whose GetStackStatuses
// answers with a preconfigured snapshot, letting applyLiveStatus's two branches
// (project present in / absent from the snapshot) be driven independently —
// unlike fakeStackDocker above, whose GetStackStatuses always returns an empty
// map.
type statusFakeDocker struct {
	statuses map[string]services.LiveStatus
}

func (f *statusFakeDocker) GetStackStatuses(context.Context, services.DashboardDB) (map[string]services.LiveStatus, error) {
	return f.statuses, nil
}
func (f *statusFakeDocker) StartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *statusFakeDocker) StopVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *statusFakeDocker) RestartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *statusFakeDocker) PullVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *statusFakeDocker) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}

// TestStacksHandler_Get_ContainerlessStackEmitsEmptyArray is the
// SEEN-FAILING-FIRST test for agent-os-kqp6: applyLiveStatus's container-less
// branch (stacks.go) used to leave stack.Containers nil, which json.Marshal
// renders as "containers":null. Asserted on the RAW RESPONSE BYTES —
// unmarshalling into a Go []Container would erase the null/[] distinction (a
// nil slice and an empty slice decode identically into the same []Container{}),
// so this inspects w.Body directly instead. Two-sided: a stack WITH containers
// (present in the docker status snapshot) must stay green.
func TestStacksHandler_Get_ContainerlessStackEmitsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run(`container-less stack: "containers":[] not "containers":null`, func(t *testing.T) {
		store := &fakeStackStore{stack: &models.Stack{ID: "s1", Directory: "/tmp/test", ComposeFile: "compose.yaml", ProjectName: "s1-default"}}
		docker := &statusFakeDocker{statuses: map[string]services.LiveStatus{}} // empty snapshot: project not present
		cfg := &config.Config{StacksDir: "/tmp/test"}
		handler := NewStacksHandler(docker, nil, nil, store, cfg, nil, services.NewOperationLock())

		router := gin.New()
		router.GET("/stacks/:id", handler.Get)

		req := httptest.NewRequest(http.MethodGet, "/stacks/s1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"containers":[]`) {
			t.Fatalf(`response did not contain "containers":[] (raw bytes): %s`, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"containers":null`) {
			t.Fatalf(`response regressed to "containers":null: %s`, w.Body.String())
		}
	})

	t.Run("control: stack WITH containers stays green", func(t *testing.T) {
		store := &fakeStackStore{stack: &models.Stack{ID: "s1", Directory: "/tmp/test", ComposeFile: "compose.yaml", ProjectName: "s1-default"}}
		docker := &statusFakeDocker{statuses: map[string]services.LiveStatus{
			"s1-default": {Status: "running", Containers: []models.Container{{Name: "web", Status: "running"}}},
		}}
		cfg := &config.Config{StacksDir: "/tmp/test"}
		handler := NewStacksHandler(docker, nil, nil, store, cfg, nil, services.NewOperationLock())

		router := gin.New()
		router.GET("/stacks/:id", handler.Get)

		req := httptest.NewRequest(http.MethodGet, "/stacks/s1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"name":"web"`) {
			t.Fatalf("response did not carry the fake container: %s", w.Body.String())
		}
	})
}
