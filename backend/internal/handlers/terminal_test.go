package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTerminalHandler(t *testing.T) *TerminalHandler {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	return NewTerminalHandler(nil, db)
}

func setupTerminalRouter(handler *TerminalHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "test-secret-key-32-chars-long!!!", false)
	return router
}

func TestTerminalHandler_WS_StackNotFound(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	handler := NewTerminalHandler(nil, db)
	router := setupTerminalRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/ws/terminal/nonexistent~stack:default/container1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTerminalHandler_WS_MissingParams(t *testing.T) {
	handler := newTestTerminalHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "secret", false)

	req := httptest.NewRequest(http.MethodGet, "/api/ws/terminal//container1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusBadRequest)
}

func TestTerminalHandler_RoutesRegistered(t *testing.T) {
	handler := newTestTerminalHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "secret", false)

	routes := router.Routes()
	routePaths := make(map[string]bool)
	for _, r := range routes {
		routePaths[r.Method+":"+r.Path] = true
	}

	assert.True(t, routePaths["GET:/api/ws/terminal/:id/:container"])
}

func TestTerminalHandler_WS_RejectsNonWS(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	createTestDirectory(t, db, "/test/dir")

	stack := models.Stack{
		ID:          "test~dir:default",
		Directory:   "/test/dir",
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "dir-default",
		Status:      "running",
	}
	err = db.UpsertStack(stack)
	require.NoError(t, err)

	handler := NewTerminalHandler(nil, db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "test-secret-key-32-chars-long!!!", false)

	req := httptest.NewRequest(http.MethodGet, "/api/ws/terminal/test~dir:default/web", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
