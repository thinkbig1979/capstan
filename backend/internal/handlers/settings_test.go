package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
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
