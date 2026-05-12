package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handlerTestChecker is a fake updateChecker for handler-level tests.
// It satisfies services' unexported updateChecker interface via structural typing.
// block is closed by the test to unblock an in-flight scan; done is closed
// by CheckForUpdates when it returns, so callers can await completion.
type handlerTestChecker struct {
	block chan struct{} // close to let CheckForUpdates return
}

func (h *handlerTestChecker) CheckForUpdates(_ context.Context, _ services.DashboardDB) ([]models.ContainerUpdateInfo, error) {
	<-h.block
	return nil, nil
}

func (h *handlerTestChecker) UpdateContainer(_ context.Context, _ string, _ services.DashboardDB) (models.UpdateResult, error) {
	return models.UpdateResult{}, nil
}

func newTestResourcesHandlerWithScheduler(t *testing.T) (*ResourcesHandler, *services.SchedulerService, *handlerTestChecker) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	checker := &handlerTestChecker{
		block: make(chan struct{}),
	}
	sched := services.NewSchedulerService(checker, db, nil, nil)
	handler := NewResourcesHandler(nil, db, sched)
	return handler, sched, checker
}

func TestResourcesHandler_CheckUpdates_RefreshWithScheduler_FreshScan(t *testing.T) {
	handler, sched, checker := newTestResourcesHandlerWithScheduler(t)
	defer func() {
		close(checker.block)
		sched.Stop()
	}()
	router := setupResourcesRouter(handler)

	// First request: refresh=true with a wired scheduler and no scan in progress.
	// StartBackgroundScan sets s.scanning=true under the mutex before spawning the
	// goroutine, so IsScanning() is already true when the HTTP response returns.
	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates?refresh=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Required fields present with correct values.
	assert.True(t, response["scanning"].(bool), "scanning must be true in fresh-scan path")
	_, hasUpdates := response["updates"]
	assert.True(t, hasUpdates, "response must contain updates key")
	_, hasFromCache := response["fromCache"]
	assert.True(t, hasFromCache, "response must contain fromCache key")
	_, hasScannedAt := response["scannedAt"]
	assert.True(t, hasScannedAt, "response must contain scannedAt key")

	// status and message must NOT be present (removed in recent refactor).
	_, hasStatus := response["status"]
	assert.False(t, hasStatus, "response must not contain status key")
	_, hasMessage := response["message"]
	assert.False(t, hasMessage, "response must not contain message key")
}

func TestResourcesHandler_CheckUpdates_RefreshWithScheduler_AlreadyInProgress(t *testing.T) {
	handler, sched, checker := newTestResourcesHandlerWithScheduler(t)
	defer func() {
		close(checker.block)
		sched.Stop()
	}()
	router := setupResourcesRouter(handler)

	// First request starts a scan; StartBackgroundScan sets scanning=true before
	// returning, so the second request can hit the already-in-progress branch
	// immediately after the first response is received.
	req1 := httptest.NewRequest(http.MethodGet, "/api/resources/updates?refresh=true", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusAccepted, w1.Code)
	assert.True(t, sched.IsScanning(), "expected IsScanning after first refresh request")

	// Second request while scan is in progress.
	req2 := httptest.NewRequest(http.MethodGet, "/api/resources/updates?refresh=true", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusAccepted, w2.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w2.Body.Bytes(), &response)
	require.NoError(t, err)

	// Both paths return the same shape with scanning:true.
	assert.True(t, response["scanning"].(bool), "scanning must be true in already-in-progress path")
	_, hasUpdates := response["updates"]
	assert.True(t, hasUpdates, "response must contain updates key")
	_, hasFromCache := response["fromCache"]
	assert.True(t, hasFromCache, "response must contain fromCache key")

	// status and message must NOT be present.
	_, hasStatus := response["status"]
	assert.False(t, hasStatus, "response must not contain status key")
	_, hasMessage := response["message"]
	assert.False(t, hasMessage, "response must not contain message key")
}
