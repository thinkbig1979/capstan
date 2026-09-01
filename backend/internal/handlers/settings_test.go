package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

func setupSettingsRouter(handler *SettingsHandler) *gin.Engine {
	router := gin.New()
	router.GET("/settings/log-retention", handler.GetLogRetention)
	router.PUT("/settings/log-retention", handler.UpdateLogRetention)
	return router
}

func TestSettingsHandler_GetLogRetention_Default(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/settings/log-retention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, float64(90), response["retentionDays"])
}

func TestSettingsHandler_GetLogRetention_Custom(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	err = db.SetSetting("max_log_retention_days", "60")
	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/settings/log-retention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, float64(60), response["retentionDays"])
}

func TestSettingsHandler_UpdateLogRetention(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": 45}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	value, err := db.GetSetting("max_log_retention_days")
	require.NoError(t, err)
	assert.Equal(t, "45", value)
}

func TestSettingsHandler_UpdateLogRetention_Minimum(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": 7}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	value, err := db.GetSetting("max_log_retention_days")
	require.NoError(t, err)
	assert.Equal(t, "7", value)
}

func TestSettingsHandler_UpdateLogRetention_BelowMinimum(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": 5}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response models.AppError
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "VALIDATION_ERROR", response.Code)
}

func TestSettingsHandler_UpdateLogRetention_InvalidInput(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": "invalid"}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func setupUpdateSettingsRouter(handler *SettingsHandler) *gin.Engine {
	router := gin.New()
	router.GET("/settings/updates", handler.GetUpdateSettings)
	router.PUT("/settings/updates", handler.UpdateUpdateSettings)
	return router
}

// newUpdateSettingsFixture wires PUT /settings/updates to a real SchedulerService
// so the scheduler side effects of a partial payload are observable via IsRunning().
func newUpdateSettingsFixture(t *testing.T) (*gin.Engine, *database.DB, *services.SchedulerService) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	checker := &handlerTestChecker{block: make(chan struct{})}
	sched := services.NewSchedulerService(checker, db, nil, nil)
	t.Cleanup(func() {
		close(checker.block)
		sched.Stop()
	})

	handler := NewSettingsHandler(db, "", "test-secret", false, sched, nil)
	return setupUpdateSettingsRouter(handler), db, sched
}

func putUpdateSettings(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/settings/updates", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// Control: a full payload still applies both values and still starts the scheduler.
func TestSettingsHandler_UpdateUpdateSettings_FullPayloadApplies(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":60,"globalAutoUpdate":true}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval)
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate)
	assert.True(t, sched.IsRunning(), "a supplied non-zero interval must start the scheduler")
}

// Control: a full payload asking for interval 0 still stops the scheduler.
func TestSettingsHandler_UpdateUpdateSettings_FullPayloadDisables(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":0,"globalAutoUpdate":false}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "0", interval)
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "false", autoUpdate)
	assert.False(t, sched.IsRunning(), "an explicit interval of 0 must stop the scheduler")
}

// Regression (agent-os-mtbo.8): a PUT that omits both keys must change nothing.
func TestSettingsHandler_UpdateUpdateSettings_PartialPayloadPreservesBoth(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"applyMode":"scheduled"}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval, "an absent scanIntervalMinutes must not reset the interval")
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate, "an absent globalAutoUpdate must not disable auto-update")
	assert.True(t, sched.IsRunning(), "an absent scanIntervalMinutes must not stop the scheduler")

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, float64(60), response["scanIntervalMinutes"])
	assert.Equal(t, true, response["globalAutoUpdate"])
}

// Regression (agent-os-mtbo.8): supplying only globalAutoUpdate must leave the
// interval and the running scheduler alone.
func TestSettingsHandler_UpdateUpdateSettings_AutoUpdateOnlyPreservesInterval(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "false"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"globalAutoUpdate":true}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval, "an absent scanIntervalMinutes must not reset the interval")
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate)
	assert.True(t, sched.IsRunning(), "an absent scanIntervalMinutes must not stop the scheduler")
}

// Regression (agent-os-mtbo.8): supplying only scanIntervalMinutes must leave
// auto-update alone.
func TestSettingsHandler_UpdateUpdateSettings_IntervalOnlyPreservesAutoUpdate(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "30"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":60}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval)
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate, "an absent globalAutoUpdate must not disable auto-update")
	assert.True(t, sched.IsRunning(), "a changed non-zero interval must restart the scheduler")
}
