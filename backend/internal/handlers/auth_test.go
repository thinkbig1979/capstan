package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthHandler_Status_NeedsSetup(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["needsSetup"].(bool))
	assert.False(t, response["authDisabled"].(bool))
}

func TestAuthHandler_Status_NoSetupNeeded(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	createTestUser(t, db, "testuser", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response["needsSetup"].(bool))
}

func TestAuthHandler_Status_AuthDisabled(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	handler := NewAuthHandler(db, "test-secret-key-32-chars", true)
	router := setupTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["authDisabled"].(bool))
}

func TestAuthHandler_Setup_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	reqBody := `{"username": "admin", "password": "SecurePass123!"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/setup", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotEmpty(t, response["token"])
	userData := response["user"].(map[string]interface{})
	assert.Equal(t, "admin", userData["username"])
	assert.NotEmpty(t, userData["id"])
}

func TestAuthHandler_Setup_AlreadyDone(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	createTestUser(t, db, "existing", "password")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	reqBody := `{"username": "admin", "password": "SecurePass123!"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/setup", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "SETUP_ALREADY_DONE", response["code"])
}

func TestAuthHandler_Setup_InvalidUsername(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	reqBody := `{"username": "ab", "password": "SecurePass123!"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/setup", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "VALIDATION_ERROR", response["code"])
}

func TestAuthHandler_Setup_InvalidPassword(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	reqBody := `{"username": "admin", "password": "short"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/setup", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "VALIDATION_ERROR", response["code"])
}

func TestAuthHandler_Login_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	createTestUser(t, db, "testuser", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	reqBody := `{"username": "testuser", "password": "password123"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotEmpty(t, response["token"])
	userData := response["user"].(map[string]interface{})
	assert.Equal(t, "testuser", userData["username"])
}

func TestAuthHandler_Login_InvalidCredentials(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	createTestUser(t, db, "testuser", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	reqBody := `{"username": "testuser", "password": "wrongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "UNAUTHORIZED", response["code"])
}

func TestAuthHandler_Logout_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	user := createTestUser(t, db, "testuser", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouterWithAuth(handler, "test-secret-key-32-chars")

	token := generateTestToken(user.ID, user.Username, "session-123", "test-secret-key-32-chars")

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set("Authorization", authHeader(token))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAuthHandler_Me_Success(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	user := createTestUser(t, db, "testuser", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouterWithAuth(handler, "test-secret-key-32-chars")

	token := generateTestToken(user.ID, user.Username, "session-123", "test-secret-key-32-chars")

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", authHeader(token))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, user.ID, response["id"])
	assert.Equal(t, user.Username, response["username"])
}

func TestAuthHandler_Me_Unauthenticated(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "UNAUTHORIZED", response["code"])
}

func generateTestToken(userID, username, sessionID, secret string) string {
	claims := jwt.MapClaims{
		"sub":      userID,
		"username": username,
		"jti":      sessionID,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return ""
	}
	return signed
}
