package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoriesHandler_List_Success(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/tmp/test/stack1",
		Name:      "stack1",
		IsGitRepo: false,
		ScannedAt: testTime,
	}
	err = db.UpsertDirectory(dir)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/directories", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/directories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	directories := response["directories"].([]interface{})
	assert.Len(t, directories, 1)
}

func TestDirectoriesHandler_Scan_Success(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.New(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/directories/scan", handler.Scan)

	req := httptest.NewRequest(http.MethodPost, "/directories/scan", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotNil(t, response["directories"])
	assert.NotNil(t, response["hasGlobalEnv"])
	assert.NotNil(t, response["scannedAt"])
}

func TestDirectoriesHandler_Get_Success(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	dir := models.Directory{
		Path:      "/tmp/test/stack1",
		Name:      "stack1",
		IsGitRepo: false,
		ScannedAt: testTime,
	}
	err = db.UpsertDirectory(dir)
	require.NoError(t, err)

	stack := models.Stack{
		ID:          "stack1:default",
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
	router.GET("/directories/:path", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/directories/tmp/test/stack1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, dir.Path, response["path"])
	stacks := response["stacks"].([]interface{})
	assert.Len(t, stacks, 1)
}

func TestDirectoriesHandler_Get_NotFound(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: "/tmp/test"}
	scanner := services.NewScannerService(cfg, db)
	handler := NewDirectoriesHandler(scanner, db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/directories/:path", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/directories/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "NOT_FOUND", response["code"])
}
