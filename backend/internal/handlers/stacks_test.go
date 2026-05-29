package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStacksHandler_Create_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	scanner := services.NewScannerService(cfg, db)
	handler := NewStacksHandler(nil, scanner, linter, db, cfg, services.NewOperationLock())

	gin.SetMode(gin.TestMode)
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

	stack := response["stack"].(map[string]interface{})
	assert.Equal(t, filepath.Base(tempDir)+"~my-stack:default", stack["id"])

	assert.FileExists(t, filepath.Join(tempDir, "my-stack", "compose.yaml"))
	assert.FileExists(t, filepath.Join(tempDir, "my-stack", ".env"))
}

func TestStacksHandler_Create_ValidationError(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	handler := NewStacksHandler(nil, nil, linter, db, cfg, services.NewOperationLock())

	gin.SetMode(gin.TestMode)
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
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewOperationLock())

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

	gin.SetMode(gin.TestMode)
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

func TestStacksHandler_Get_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewOperationLock())

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

	gin.SetMode(gin.TestMode)
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
	handler := NewStacksHandler(nil, nil, nil, db, cfg, services.NewOperationLock())

	gin.SetMode(gin.TestMode)
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
