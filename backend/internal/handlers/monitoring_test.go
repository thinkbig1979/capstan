package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMonitoringHandler(t *testing.T) *MonitoringHandler {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cm := NewConnectionManager(10)
	return NewMonitoringHandler(nil, nil, db, cm, NewEventBus())
}

func setupMonitoringRouter(handler *MonitoringHandler) *gin.Engine {
	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "test-secret-key-32-chars-long!!!", false)
	return router
}

func TestMonitoringHandler_GetStackContainers_NotFound(t *testing.T) {
	handler := newTestMonitoringHandler(t)
	router := setupMonitoringRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/stacks/nonexistent/containers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, models.ErrNotFound, response["code"])
}

func TestMonitoringHandler_GetStackContainers_WithStack_NoDocker(t *testing.T) {
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

	cm := NewConnectionManager(10)
	handler := NewMonitoringHandler(nil, nil, db, cm, NewEventBus())

	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "test-secret-key-32-chars-long!!!", false)

	req := httptest.NewRequest(http.MethodGet, "/api/stacks/test~dir:default/containers", nil)
	w := httptest.NewRecorder()

	// The nil Docker service used to be dereferenced here and panic. It now
	// refuses; the status this becomes is asserted once the handlers map the
	// outage sentinel to 503 (agent-os-xay).
	require.NotPanics(t, func() {
		router.ServeHTTP(w, req)
	})
}

func TestMonitoringHandler_MetricsWS_StackNotFound(t *testing.T) {
	handler := newTestMonitoringHandler(t)
	router := setupMonitoringRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/ws/metrics/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMonitoringHandler_EventsWS_RejectsNonWS(t *testing.T) {
	handler := newTestMonitoringHandler(t)
	router := setupMonitoringRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/ws/events", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMonitoringHandler_RoutesRegistered(t *testing.T) {
	handler := newTestMonitoringHandler(t)

	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group, "secret", false)

	routes := router.Routes()
	routePaths := make(map[string]bool)
	for _, r := range routes {
		routePaths[r.Method+":"+r.Path] = true
	}

	assert.True(t, routePaths["GET:/api/stacks/:id/containers"])
	assert.True(t, routePaths["GET:/api/ws/metrics/:id"])
	assert.True(t, routePaths["GET:/api/ws/events"])
}

func TestBroadcastEvent_DoesNotBlock(t *testing.T) {
	event := models.StackEvent{
		Type:      "stack_changed",
		StackID:   "test~stack:default",
		Timestamp: testTime,
	}

	BroadcastEvent(event)
}

func TestBroadcastEvent_MultipleEvents(t *testing.T) {
	for i := 0; i < 10; i++ {
		event := models.StackEvent{
			Type:      "test_event",
			Timestamp: testTime,
		}
		BroadcastEvent(event)
	}
}
