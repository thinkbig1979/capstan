package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSettingsFullRouter(handler *SettingsHandler) *gin.Engine {
	router := gin.New()
	router.PUT("/auth/password", authContextMiddleware("test-user-id"), handler.ChangePassword)
	router.GET("/settings/config", handler.GetConfig)
	router.GET("/settings/global-env", handler.GetGlobalEnv)
	router.PUT("/settings/global-env", handler.UpdateGlobalEnv)
	router.GET("/settings/log-retention", handler.GetLogRetention)
	router.PUT("/settings/log-retention", handler.UpdateLogRetention)
	router.GET("/settings/updates", handler.GetUpdateSettings)
	router.PUT("/settings/updates", handler.UpdateUpdateSettings)
	router.GET("/settings/git", handler.GetGitSettings)
	router.PUT("/settings/git", handler.UpdateGitSettings)
	router.GET("/settings/directories", handler.GetConfiguredDirectories)
	router.PUT("/settings/directories", handler.UpdateConfiguredDirectories)
	return router
}

func newTestSettingsHandler(t *testing.T) (*SettingsHandler, *gin.Engine) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{
		StacksDir:    "/opt/stacks",
		DataDir:      t.TempDir(),
		GitSSHKey:    "/default/key",
		GitHTTPSUser: "git",
	}

	handler := NewSettingsHandler(db, "/opt/stacks", "test-secret-key-32-chars-long!!!", false, nil, cfg)
	router := setupSettingsFullRouter(handler)
	return handler, router
}

func TestSettingsHandler_ChangePassword_AuthDisabled(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	handler := NewSettingsHandler(db, "", "test-secret", true, nil, nil)
	router := gin.New()
	router.PUT("/auth/password", handler.ChangePassword)

	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(`{"currentPassword":"old","newPassword":"NewPass123!"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSettingsHandler_ChangePassword_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "testuser", "OldPassword123!")
	handler := NewSettingsHandler(db, "", "test-secret-key-32-chars-long!!!", false, nil, nil)

	router := gin.New()
	router.PUT("/auth/password", authContextMiddleware(user.ID), handler.ChangePassword)

	body := `{"currentPassword":"OldPassword123!","newPassword":"NewPassword456!"}`
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestSettingsHandler_ChangePassword_WrongCurrentPassword(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "testuser", "OldPassword123!")
	handler := NewSettingsHandler(db, "", "test-secret-key-32-chars-long!!!", false, nil, nil)

	router := gin.New()
	router.PUT("/auth/password", authContextMiddleware(user.ID), handler.ChangePassword)

	body := `{"currentPassword":"WrongPassword123!","newPassword":"NewPassword456!"}`
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSettingsHandler_ChangePassword_InvalidBody(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "testuser", "OldPassword123!")
	handler := NewSettingsHandler(db, "", "test-secret-key-32-chars-long!!!", false, nil, nil)

	router := gin.New()
	router.PUT("/auth/password", authContextMiddleware(user.ID), handler.ChangePassword)

	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSettingsHandler_ChangePassword_NoAuth(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	handler := NewSettingsHandler(db, "", "test-secret-key-32-chars-long!!!", false, nil, nil)

	router := gin.New()
	router.PUT("/auth/password", handler.ChangePassword)

	body := `{"currentPassword":"old","newPassword":"NewPass123!"}`
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSettingsHandler_GetConfig(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "/opt/stacks", response["stacksDir"])
}

func TestSettingsHandler_GetGlobalEnv_Empty(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	vars := response["vars"].([]interface{})
	assert.Len(t, vars, 0)
}

func TestSettingsHandler_UpdateAndGetGlobalEnv(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"vars":[{"key":"FOO","value":"bar"},{"key":"BAZ","value":"qux"}]}`
	req := httptest.NewRequest(http.MethodPut, "/settings/global-env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	req = httptest.NewRequest(http.MethodGet, "/settings/global-env", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	vars := response["vars"].([]interface{})
	assert.Len(t, vars, 2)
}

func TestSettingsHandler_UpdateGlobalEnv_EmptyKey(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"vars":[{"key":"","value":"bar"}]}`
	req := httptest.NewRequest(http.MethodPut, "/settings/global-env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSettingsHandler_UpdateGlobalEnv_Newlines(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"vars":[{"key":"FOO\nBAR","value":"baz"}]}`
	req := httptest.NewRequest(http.MethodPut, "/settings/global-env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSettingsHandler_GetUpdateSettings_Default(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/updates", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, float64(0), response["scanIntervalMinutes"])
	assert.Equal(t, false, response["globalAutoUpdate"])
}

func TestSettingsHandler_UpdateUpdateSettings(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"scanIntervalMinutes":30,"globalAutoUpdate":true}`
	req := httptest.NewRequest(http.MethodPut, "/settings/updates", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, float64(30), response["scanIntervalMinutes"])
	assert.Equal(t, true, response["globalAutoUpdate"])
}

func TestSettingsHandler_UpdateUpdateSettings_IntervalTooLow(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"scanIntervalMinutes":5,"globalAutoUpdate":false}`
	req := httptest.NewRequest(http.MethodPut, "/settings/updates", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSettingsHandler_GetGitSettings_Default(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/git", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "/default/key", response["sshKey"])
	assert.Equal(t, "git", response["httpsUser"])
	assert.Equal(t, false, response["hasHttpsToken"])
}

func TestSettingsHandler_UpdateGitSettings(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"sshKey":"/new/key","httpsUser":"newuser","httpsToken":"newtoken"}`
	req := httptest.NewRequest(http.MethodPut, "/settings/git", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "/new/key", response["sshKey"])
	assert.Equal(t, "newuser", response["httpsUser"])
	assert.Equal(t, true, response["hasHttpsToken"])
}

func TestSettingsHandler_GetConfiguredDirectories(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/directories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "/opt/stacks", response["defaultDir"])

	dirs := response["directories"].([]interface{})
	require.Len(t, dirs, 1)
	dir := dirs[0].(map[string]interface{})
	assert.Equal(t, "/opt/stacks", dir["path"])
	assert.Equal(t, true, dir["isDefault"])
}

func TestSettingsHandler_UpdateConfiguredDirectories_InvalidPath(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"defaultDir":"/not/allowed/path"}`
	req := httptest.NewRequest(http.MethodPut, "/settings/directories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSettingsHandler_UpdateConfiguredDirectories_ValidPath(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	body := `{"defaultDir":"/opt/stacks"}`
	req := httptest.NewRequest(http.MethodPut, "/settings/directories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSettingsHandler_UpdateConfiguredDirectories_InvalidBody(t *testing.T) {
	_, router := newTestSettingsHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/settings/directories", strings.NewReader(`invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSettingsParseEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := tmpDir + "/test.env"

	content := "DB_HOST=localhost\nDB_PORT=5432\n# comment\n\nAPI_KEY=secret123\n"
	err := writeTestFile(envPath, content)
	require.NoError(t, err)

	vars, err := parseEnvFile(envPath)
	require.NoError(t, err)
	require.Len(t, vars, 3)
	assert.Equal(t, "DB_HOST", vars[0]["key"])
	assert.Equal(t, "localhost", vars[0]["value"])
	assert.Equal(t, "DB_PORT", vars[1]["key"])
	assert.Equal(t, "5432", vars[1]["value"])
	assert.Equal(t, "API_KEY", vars[2]["key"])
	assert.Equal(t, "secret123", vars[2]["value"])
}

func TestSettingsParseEnvFile_Nonexistent(t *testing.T) {
	vars, err := parseEnvFile("/nonexistent/path.env")
	assert.NoError(t, err)
	assert.Len(t, vars, 0)
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}
