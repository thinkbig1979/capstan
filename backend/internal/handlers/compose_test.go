package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeHandler_Get_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	composePath := filepath.Join(stackDir, "compose.yaml")
	composeContent := "services:\n  web:\n    image: nginx:1.21"
	err = os.WriteFile(composePath, []byte(composeContent), 0644)
	require.NoError(t, err)

	createTestDirectory(t, db, stackDir)

	stack := models.Stack{
		ID:          "stack1:default",
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	handler := NewComposeHandler(linter, db, cfg)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stacks/:id/compose", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/stack1:default/compose", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, composeContent, response["content"])
	assert.Equal(t, "compose.yaml", response["filename"])
	assert.NotNil(t, response["size"])
	assert.NotNil(t, response["lastModified"])
}

func TestComposeHandler_Get_NotFound(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	linter := services.NewLinterService()
	handler := NewComposeHandler(linter, db, cfg)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stacks/:id/compose", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/nonexistent:default/compose", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "STACK_NOT_FOUND", response["code"])
}

func TestComposeHandler_Put_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	composePath := filepath.Join(stackDir, "compose.yaml")
	composeContent := "services:\n  web:\n    image: nginx:1.21"
	err = os.WriteFile(composePath, []byte(composeContent), 0644)
	require.NoError(t, err)

	createTestDirectory(t, db, stackDir)

	stack := models.Stack{
		ID:          "stack1:default",
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	handler := NewComposeHandler(linter, db, cfg)

	newContent := "services:\n  web:\n    image: nginx:1.22"
	reqBody := map[string]string{"content": newContent}
	reqBytes, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/stacks/:id/compose", authContextMiddleware("test-user-id"), handler.Put)

	req := httptest.NewRequest(http.MethodPut, "/stacks/stack1:default/compose", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["saved"].(bool))

	savedContent, err := os.ReadFile(composePath)
	require.NoError(t, err)
	assert.Equal(t, newContent, string(savedContent))
}

func TestComposeHandler_Put_ValidationError(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	composePath := filepath.Join(stackDir, "compose.yaml")
	composeContent := "services:\n  web:\n    image: nginx:1.21"
	err = os.WriteFile(composePath, []byte(composeContent), 0644)
	require.NoError(t, err)

	createTestDirectory(t, db, stackDir)

	stack := models.Stack{
		ID:          "stack1:default",
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	linter := services.NewLinterService()
	handler := NewComposeHandler(linter, db, cfg)

	invalidContent := "services:\n  web:\n    invalid: ["
	reqBody := map[string]string{"content": invalidContent}
	reqBytes, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/stacks/:id/compose", authContextMiddleware("test-user-id"), handler.Put)

	req := httptest.NewRequest(http.MethodPut, "/stacks/stack1:default/compose", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	savedContent, err := os.ReadFile(composePath)
	require.NoError(t, err)
	assert.Equal(t, composeContent, string(savedContent))
}

func TestComposeHandler_Lint_Valid(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	linter := services.NewLinterService()
	handler := NewComposeHandler(linter, db, cfg)

	content := "services:\n  web:\n    image: nginx:1.21\n    restart: unless-stopped"
	reqBody := map[string]string{"content": content}
	reqBytes, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/stacks/:id/compose/lint", handler.Lint)

	req := httptest.NewRequest(http.MethodPost, "/stacks/any/compose/lint", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["valid"].(bool))
}

func TestComposeHandler_validatePath(t *testing.T) {
	cfg := &config.Config{StacksDir: "/opt/stacks"}
	linter := services.NewLinterService()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	handler := NewComposeHandler(linter, db, cfg)

	tests := []struct {
		name        string
		path        string
		expectError bool
	}{
		{
			name:        "valid path within stacks dir",
			path:        "/opt/stacks/test-stack/compose.yaml",
			expectError: false,
		},
		{
			name:        "path traversal escapes stacks dir",
			path:        "/opt/stacks/../../etc/passwd",
			expectError: true,
		},
		{
			name:        "valid path in nested subdirectory",
			path:        "/opt/stacks/myapp/config/compose.yaml",
			expectError: false,
		},
		{
			name:        "path traversal with intermediate dots",
			path:        "/opt/stacks/../etc/shadow",
			expectError: true,
		},
		{
			name:        "absolute path outside stacks dir",
			path:        "/etc/passwd",
			expectError: true,
		},
		{
			name:        "valid path at stacks dir root",
			path:        "/opt/stacks/compose.yaml",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validatePath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
				var appErr *models.AppError
				assert.ErrorAs(t, err, &appErr)
				assert.Equal(t, models.ErrPathTraversal, appErr.Code)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEnvHandler_validatePath(t *testing.T) {
	cfg := &config.Config{StacksDir: "/opt/stacks"}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	handler := NewEnvHandler(db, cfg)

	tests := []struct {
		name        string
		path        string
		expectError bool
	}{
		{
			name:        "valid env path within stacks dir",
			path:        "/opt/stacks/test-stack/.env",
			expectError: false,
		},
		{
			name:        "path traversal escapes stacks dir",
			path:        "/opt/stacks/../../etc/passwd",
			expectError: true,
		},
		{
			name:        "valid path in nested subdirectory",
			path:        "/opt/stacks/myapp/config/.env",
			expectError: false,
		},
		{
			name:        "absolute path outside stacks dir",
			path:        "/etc/shadow",
			expectError: true,
		},
		{
			name:        "valid path at stacks dir root",
			path:        "/opt/stacks/.env",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validatePath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
				var appErr *models.AppError
				assert.ErrorAs(t, err, &appErr)
				assert.Equal(t, models.ErrPathTraversal, appErr.Code)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestComposeHandler_Lint_Invalid(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	linter := services.NewLinterService()
	handler := NewComposeHandler(linter, db, cfg)

	content := "services:\n  web:\n    restart: unless-stopped"
	reqBody := map[string]string{"content": content}
	reqBytes, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/stacks/:id/compose/lint", handler.Lint)

	req := httptest.NewRequest(http.MethodPost, "/stacks/any/compose/lint", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response["valid"].(bool))
	lintResults := response["lintResults"].([]interface{})
	assert.Greater(t, len(lintResults), 0)
}
