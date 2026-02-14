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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvHandler_Get_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.New(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	envPath := filepath.Join(stackDir, ".env")
	envContent := "DATABASE_URL=postgres://localhost:5432/mydb\nPORT=8080"
	err = os.WriteFile(envPath, []byte(envContent), 0644)
	require.NoError(t, err)

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
	handler := NewEnvHandler(db, cfg)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stacks/:id/env", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/stack1:default/env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ".env", response["filename"])
	assert.Equal(t, envContent, response["raw"])
	assert.NotNil(t, response["entries"])
}

func TestEnvHandler_Get_NoEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.New(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	stack := models.Stack{
		ID:          "stack1:default",
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     "",
		ProjectName: "stack1-default",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	handler := NewEnvHandler(db, cfg)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stacks/:id/env", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/stacks/stack1:default/env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "NOT_FOUND", response["code"])
}

func TestEnvHandler_Put_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.New(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	envPath := filepath.Join(stackDir, ".env")
	envContent := "DATABASE_URL=postgres://localhost:5432/mydb\nPORT=8080"
	err = os.WriteFile(envPath, []byte(envContent), 0644)
	require.NoError(t, err)

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
	handler := NewEnvHandler(db, cfg)

	newContent := "DATABASE_URL=postgres://localhost:5432/mydb\nPORT=9090"
	reqBody := map[string]string{"raw": newContent}
	reqBytes, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/stacks/:id/env", handler.Put)

	req := httptest.NewRequest(http.MethodPut, "/stacks/stack1:default/env", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["saved"].(bool))
	assert.Equal(t, ".env", response["filename"])

	savedContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, string(savedContent))
}

func TestEnvHandler_Put_WithEntries(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.New(":memory:")
	require.NoError(t, err)

	stackDir := filepath.Join(tempDir, "stack1")
	os.MkdirAll(stackDir, 0755)

	envPath := filepath.Join(stackDir, ".env")
	envContent := "DATABASE_URL=postgres://localhost:5432/mydb\nPORT=8080"
	err = os.WriteFile(envPath, []byte(envContent), 0644)
	require.NoError(t, err)

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
	handler := NewEnvHandler(db, cfg)

	entries := []map[string]interface{}{
		{"key": "DATABASE_URL", "value": "postgres://localhost:5432/mydb", "line": 1, "comment": false},
		{"key": "PORT", "value": "9090", "line": 2, "comment": false},
	}
	reqBody := map[string]interface{}{"entries": entries}
	reqBytes, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/stacks/:id/env", handler.Put)

	req := httptest.NewRequest(http.MethodPut, "/stacks/stack1:default/env", bytes.NewReader(reqBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	savedContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	assert.Contains(t, string(savedContent), "PORT=9090")
}
