package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
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

// TestAuthHandler_Login_CaseInsensitiveUsername pins agent-os-tmo end to end
// through the real HTTP handler, not just at the DB layer: a user who
// registered as "Admin" must be able to log in typing any casing of that
// username, and the account's originally-stored casing (not the casing
// typed at login) must come back in the response.
func TestAuthHandler_Login_CaseInsensitiveUsername(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	createTestUser(t, db, "Admin", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	for _, typedUsername := range []string{"Admin", "admin", "ADMIN", "aDmIn"} {
		reqBody := `{"username": "` + typedUsername + `", "password": "password123"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "login must succeed typing %q when the account is stored as \"Admin\"", typedUsername)

		var response map[string]interface{}
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.NotEmpty(t, response["token"], "typed %q", typedUsername)
		userData := response["user"].(map[string]interface{})
		assert.Equal(t, "Admin", userData["username"], "typed %q: the account's stored casing must be returned, not the casing typed at login", typedUsername)
	}
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

// TestAuthHandler_Login_UnknownUserPerformsBcrypt is the regression test for
// H3: the login handler must perform a bcrypt comparison even when the username
// does not exist, so an attacker cannot enumerate valid usernames by response
// latency. A bcrypt comparison at the default cost takes tens of milliseconds;
// the pre-fix no-user path returned in microseconds. We assert the fastest
// unknown-user attempt still spends a bcrypt-sized amount of time — a huge,
// stable gap, not a flaky micro-threshold.
func TestAuthHandler_Login_UnknownUserPerformsBcrypt(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)

	createTestUser(t, db, "realuser", "password123")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	measure := func(username string) time.Duration {
		body := `{"username": "` + username + `", "password": "somewrongpassword"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		start := time.Now()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		return time.Since(start)
	}

	// Take the minimum across several attempts: if even the fastest unknown-user
	// login took longer than 1ms, a bcrypt comparison definitely ran. bcrypt at
	// DefaultCost is well over 1ms on any hardware; the pre-fix path was sub-100µs.
	unknownMin := time.Hour
	for i := 0; i < 5; i++ {
		if d := measure("nonexistent-user-xyz"); d < unknownMin {
			unknownMin = d
		}
	}

	assert.Greater(t, unknownMin, time.Millisecond,
		"unknown-username login must still perform a bcrypt comparison (constant-time defense, H3)")
}

// TestAuthHandler_Login_SecureCookieFromForwardedProto is the regression test
// for M3: the Secure cookie flag must follow the real request scheme
// (X-Forwarded-Proto from a TLS-terminating proxy), not a Host substring.
func TestAuthHandler_Login_SecureCookieFromForwardedProto(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	createTestUser(t, db, "testuser", "password123")
	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	body := `{"username": "testuser", "password": "password123"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var sawToken, secure bool
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "capstan_token" {
			sawToken = true
			secure = ck.Secure
		}
	}
	require.True(t, sawToken, "expected capstan_token cookie")
	assert.True(t, secure, "Secure flag must be set when X-Forwarded-Proto is https")
}

func TestLooksLikePrivateKey(t *testing.T) {
	keyMaterial := []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"line1\nline2",
	}
	for _, s := range keyMaterial {
		assert.True(t, looksLikePrivateKey(s), "should detect key material: %q", s)
	}
	paths := []string{"/root/.ssh/id_rsa", "/home/user/.ssh/id_ed25519", "~/.ssh/key"}
	for _, p := range paths {
		assert.False(t, looksLikePrivateKey(p), "path must be allowed: %q", p)
	}
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
		"iss":      jwtIssuer,
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
