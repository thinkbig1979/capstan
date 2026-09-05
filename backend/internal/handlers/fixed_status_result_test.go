package handlers

import (
	"bytes"
	"context"
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
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// These tests pin the status AND outcome of the ActionResult responses that
// carry a fixed status rather than HTTPStatus()'s derived one (agent-os-oqca):
// 409 for "already exists", 201 for creates, 207 for created-but-not-deployed.
// Each of those sites now goes through renderResultWithStatus, and these pins
// are what makes that routing checkable: an overlay that changes what the seam
// writes turns every one of them red, and leaves them green at the pre-fix
// base. The two remaining sites (stack_crud.go's no-deploy 201 and
// deploy-succeeded 201) are pinned by TestStacksHandler_Create_Success and
// TestStacksHandler_CreateWithDeploy_ProjectNameMatchesPersistedRow.

// decodeActionResult returns the outcome and details of an ActionResult body.
func decodeActionResult(t *testing.T, body []byte) (string, map[string]interface{}) {
	t.Helper()
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &resp), "body: %s", body)
	outcome, _ := resp["outcome"].(string)
	details, _ := resp["details"].(map[string]interface{})
	return outcome, details
}

// seedStackOnDisk creates a stack directory under tempDir, registers it in db
// and returns the stack. envOnDisk controls whether a .env file already exists.
func seedStackOnDisk(t *testing.T, db *database.DB, tempDir string, envOnDisk bool) models.Stack {
	t.Helper()
	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx:1.21\n"), 0644))
	if envOnDisk {
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("PORT=8080\n"), 0600))
	}
	createTestDirectory(t, db, stackDir)

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~stack1:default",
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}
	require.NoError(t, db.UpsertStack(stack))
	return stack
}

// TestComposeHandler_PutComposeAndEnv_Success pins compose.go's
// PutComposeAndEnv success response: 200 with outcome success.
func TestComposeHandler_PutComposeAndEnv_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	stack := seedStackOnDisk(t, db, tempDir, true)

	handler := NewComposeHandler(services.NewLinterService(), db, &config.Config{StacksDir: tempDir})
	router := gin.New()
	router.PUT("/stacks/:id/compose-env", authContextMiddleware("test-user-id"), envUnlockedMiddleware(), handler.PutComposeAndEnv)

	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stack.ID+"/compose-env",
		strings.NewReader(`{"composeContent":"services:\n  web:\n    image: nginx:1.22\n","envRaw":"PORT=9090\n"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	outcome, details := decodeActionResult(t, w.Body.Bytes())
	assert.Equal(t, "success", outcome)
	assert.Equal(t, "compose.yaml", details["compose"])
	assert.Equal(t, ".env", details["env"])
}

// TestEnvHandler_Create_AlreadyExists pins env.go's Create refusal when the
// file is already on disk: 409, not HTTPStatus()'s 200 for no_change.
func TestEnvHandler_Create_AlreadyExists(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	stack := seedStackOnDisk(t, db, tempDir, true)

	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})
	router := gin.New()
	router.POST("/stacks/:id/env", authContextMiddleware("test-user-id"), handler.Create)

	req := httptest.NewRequest(http.MethodPost, "/stacks/"+stack.ID+"/env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	outcome, details := decodeActionResult(t, w.Body.Bytes())
	assert.Equal(t, "no_change", outcome)
	assert.Equal(t, ".env", details["filename"])
}

// TestEnvHandler_Create_Success pins env.go's Create success: 201, not
// HTTPStatus()'s 200 for success.
func TestEnvHandler_Create_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	stack := seedStackOnDisk(t, db, tempDir, false)

	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})
	router := gin.New()
	router.POST("/stacks/:id/env", authContextMiddleware("test-user-id"), handler.Create)

	req := httptest.NewRequest(http.MethodPost, "/stacks/"+stack.ID+"/env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	outcome, details := decodeActionResult(t, w.Body.Bytes())
	assert.Equal(t, "success", outcome)
	assert.Equal(t, ".env", details["filename"])
	assert.FileExists(t, filepath.Join(stack.Directory, ".env"))
}

// TestResourcesHandler_CreateNetwork_Success pins resource_mutations.go's
// createNetwork success: 201, not HTTPStatus()'s 200. ResourcesHandler holds
// the concrete *services.DockerService, so the daemon is a fake HTTP server
// answering the two calls this path makes (the constructor's ping, then
// POST /networks/create), reached the same way dashboard_metrics_close_test.go
// reaches it.
func TestResourcesHandler_CreateNetwork_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/networks/create"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"net-oqca-1","Warning":""}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	docker := newTestDockerServiceAgainst(t, srv)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	handler := NewResourcesHandler(docker, db, nil)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodPost, "/api/resources/networks",
		strings.NewReader(`{"name":"oqca-net"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	outcome, details := decodeActionResult(t, w.Body.Bytes())
	assert.Equal(t, "success", outcome)
	assert.Equal(t, "net-oqca-1", details["id"])
	assert.Equal(t, "oqca-net", details["name"])
}

// failingStartStackDocker is a stackDocker double whose StartVerified fails, so
// Create's create-with-deploy path takes its 207 branch.
type failingStartStackDocker struct{}

func (failingStartStackDocker) GetStackStatuses(context.Context, services.DashboardDB) (map[string]services.LiveStatus, error) {
	return map[string]services.LiveStatus{}, nil
}
func (failingStartStackDocker) StartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.Failed("compose up failed", assert.AnError), "compose up: boom"
}
func (failingStartStackDocker) StopVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (failingStartStackDocker) RestartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (failingStartStackDocker) PullVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (failingStartStackDocker) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}

// TestStacksHandler_Create_DeployFails_MultiStatus pins stack_crud.go's
// created-but-not-deployed response: 207 with outcome partial, and the stack
// persisted so the frontend keeps it rather than discarding it.
func TestStacksHandler_Create_DeployFails_MultiStatus(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{StacksDir: tempDir}
	handler := NewStacksHandler(failingStartStackDocker{}, services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)

	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))
	createTestDirectory(t, db, filepath.Join(tempDir, "oqca-stack"))

	reqBytes, err := json.Marshal(map[string]interface{}{
		"name":           "oqca-stack",
		"composeContent": "services:\n  web:\n    image: nginx:1.21\n",
		"deploy":         true,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusMultiStatus, w.Code, "body: %s", w.Body.String())
	outcome, details := decodeActionResult(t, w.Body.Bytes())
	assert.Equal(t, "partial", outcome)
	assert.Equal(t, false, details["deployed"])
	assert.Equal(t, assert.AnError.Error(), details["deployError"])

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1, "the stack must be persisted even though the deploy failed")
}
