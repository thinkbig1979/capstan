package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestResourcesHandler(t *testing.T) *ResourcesHandler {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	return NewResourcesHandler(nil, db, nil)
}

func setupResourcesRouter(handler *ResourcesHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))
	return router
}

func TestResourcesHandler_CheckUpdates_CachedEmpty(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	updates := response["updates"].([]interface{})
	assert.Len(t, updates, 0)
}

func TestResourcesHandler_CheckUpdates_RefreshNoDocker(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates?refresh=true", nil)
	w := httptest.NewRecorder()

	assert.Panics(t, func() {
		router.ServeHTTP(w, req)
	})
}

func TestResourcesHandler_GetUpdateHistory_Empty(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates/history", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	entries := response["entries"].([]interface{})
	assert.Len(t, entries, 0)
	assert.Equal(t, float64(0), response["total"])
	assert.Equal(t, float64(1), response["page"])
	assert.Equal(t, float64(25), response["limit"])
	assert.Equal(t, float64(0), response["totalPages"])
}

func TestResourcesHandler_GetUpdateHistory_WithPagination(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates/history?page=2&limit=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, float64(2), response["page"])
	assert.Equal(t, float64(10), response["limit"])
}

func TestResourcesHandler_ClearUpdateHistory_MissingParam(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodDelete, "/api/resources/updates/history", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "VALIDATION_ERROR", response["code"])
}

func TestResourcesHandler_ClearUpdateHistory_InvalidDate(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodDelete, "/api/resources/updates/history?olderThan=not-a-date", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResourcesHandler_ClearUpdateHistory_ValidDate(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodDelete, "/api/resources/updates/history?olderThan=2025-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, float64(0), response["deleted"])
}

func TestResourcesHandler_ListAutoUpdatePolicies_Empty(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/resources/auto-update/policies", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, false, response["globalEnabled"])
	policies := response["policies"].([]interface{})
	assert.Len(t, policies, 0)
}

func TestResourcesHandler_UpsertAutoUpdatePolicy_InvalidTargetType(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/invalid/abc123",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResourcesHandler_UpsertAutoUpdatePolicy_Container(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/container/abc123",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, true, response["enabled"])
	assert.Equal(t, "container", response["targetType"])
	assert.Equal(t, "abc123", response["targetId"])
}

func TestResourcesHandler_UpsertAutoUpdatePolicy_Stack(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/stack/stack123",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResourcesHandler_UpsertAutoUpdatePolicy_Disable(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/container/abc123",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/container/abc123",
		strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, false, response["enabled"])
	assert.Equal(t, false, response["paused"])
}

func TestResourcesHandler_UpsertThenListPolicies(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/container/c1",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/stack/s1",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/resources/auto-update/policies", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	policies := response["policies"].([]interface{})
	assert.Len(t, policies, 2)
}

func TestResourcesHandler_DeleteAutoUpdatePolicy_InvalidTargetType(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodDelete, "/api/resources/auto-update/policies/invalid/abc123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResourcesHandler_DeleteAutoUpdatePolicy_NonExistent(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodDelete, "/api/resources/auto-update/policies/container/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestResourcesHandler_DeleteAutoUpdatePolicy_Success(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update/policies/container/abc123",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodDelete, "/api/resources/auto-update/policies/container/abc123", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/resources/auto-update/policies", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	policies := response["policies"].([]interface{})
	assert.Len(t, policies, 0)
}

func TestResourcesHandler_RoutesRegistered(t *testing.T) {
	handler := newTestResourcesHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	handler.RegisterRoutes(group)

	routes := router.Routes()
	routePaths := make(map[string]bool)
	for _, r := range routes {
		routePaths[r.Method+":"+r.Path] = true
	}

	expectedRoutes := []string{
		"GET:/api/resources/images",
		"DELETE:/api/resources/images/:id",
		"POST:/api/resources/images/prune",
		"GET:/api/resources/containers",
		"POST:/api/resources/containers/:id/start",
		"POST:/api/resources/containers/:id/stop",
		"POST:/api/resources/containers/:id/restart",
		"DELETE:/api/resources/containers/:id",
		"POST:/api/resources/containers/prune",
		"GET:/api/resources/updates",
		"POST:/api/resources/containers/:id/update",
		"GET:/api/resources/updates/history",
		"DELETE:/api/resources/updates/history",
		"GET:/api/resources/auto-update/policies",
		"PUT:/api/resources/auto-update/policies/:targetType/:targetId",
		"DELETE:/api/resources/auto-update/policies/:targetType/:targetId",
		"GET:/api/resources/volumes",
		"DELETE:/api/resources/volumes/:name",
		"POST:/api/resources/volumes/prune",
		"GET:/api/resources/networks",
		"POST:/api/resources/networks",
		"DELETE:/api/resources/networks/:id",
		"POST:/api/resources/networks/prune",
		"GET:/api/resources/build-cache",
		"POST:/api/resources/build-cache/prune",
	}

	for _, route := range expectedRoutes {
		assert.True(t, routePaths[route], "Expected route %s to be registered", route)
	}
}

func TestParsePruneOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name      string
		query     string
		wantAll   bool
		wantUntil string
	}{
		{"empty defaults to basic prune", "", false, ""},
		{"all=true enables all", "all=true", true, ""},
		{"all other values are false", "all=1", false, ""},
		{"valid hour until", "until=24h", false, "24h"},
		{"valid minute until", "until=30m", false, "30m"},
		{"all and until together", "all=true&until=168h", true, "168h"},
		{"invalid until is dropped", "until=24x", false, ""},
		{"injection until is dropped", "until=24h;rm+-rf", false, ""},
		{"non-numeric until is dropped", "until=lots", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/prune?"+tc.query, nil)
			opts := parsePruneOptions(c)
			assert.Equal(t, tc.wantAll, opts.All)
			assert.Equal(t, tc.wantUntil, opts.Until)
		})
	}
}
