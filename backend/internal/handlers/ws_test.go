package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func TestConnectionManager_Add(t *testing.T) {
	cm := NewConnectionManager(2)
	conn := &Connection{
		ID:     uuid.New().String(),
		UserID: "user1",
	}

	err := cm.Add(conn.ID, conn)
	assert.NoError(t, err)
	assert.Equal(t, 1, cm.Count())
	assert.Equal(t, 1, cm.CountByUser("user1"))

	err = cm.Add(uuid.New().String(), &Connection{
		ID:     uuid.New().String(),
		UserID: "user1",
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, cm.Count())

	err = cm.Add(uuid.New().String(), &Connection{
		ID:     uuid.New().String(),
		UserID: "user1",
	})
	assert.Error(t, err)
	assert.Equal(t, 2, cm.Count())
}

func TestConnectionManager_Remove(t *testing.T) {
	cm := NewConnectionManager(10)
	connID := uuid.New().String()
	conn := &Connection{
		ID:     connID,
		UserID: "user1",
	}

	err := cm.Add(connID, conn)
	require.NoError(t, err)
	assert.Equal(t, 1, cm.Count())

	cm.Remove(connID)
	assert.Equal(t, 0, cm.Count())
	assert.Equal(t, 0, cm.CountByUser("user1"))

	cm.Remove(connID)
	assert.Equal(t, 0, cm.Count())
}

func TestConnectionManager_Get(t *testing.T) {
	cm := NewConnectionManager(10)
	connID := uuid.New().String()
	conn := &Connection{
		ID:     connID,
		UserID: "user1",
	}

	err := cm.Add(connID, conn)
	require.NoError(t, err)

	retrieved, exists := cm.Get(connID)
	assert.True(t, exists)
	assert.Equal(t, conn, retrieved)

	_, exists = cm.Get("nonexistent")
	assert.False(t, exists)
}

func TestConnectionManager_CloseAll(t *testing.T) {
	cm := NewConnectionManager(10)

	for i := 0; i < 3; i++ {
		conn := &Connection{
			ID:     uuid.New().String(),
			UserID: "user1",
		}
		require.NoError(t, cm.Add(conn.ID, conn))
	}

	assert.Equal(t, 3, cm.Count())
	assert.Equal(t, 3, cm.CountByUser("user1"))
	cm.CloseAll()
	assert.Equal(t, 0, cm.Count())
	assert.Equal(t, 0, cm.CountByUser("user1"))
}

func TestValidateJWT(t *testing.T) {
	secret := "test-secret-key-32-chars-long!!"

	claims := map[string]interface{}{
		"sub":      "user123",
		"username": "testuser",
		"jti":      "session123",
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token, err := generateJWTForTest(claims, secret)
	require.NoError(t, err)

	parsed, err := middleware.ValidateJWT(token, secret)
	assert.NoError(t, err)
	assert.Equal(t, "user123", parsed["sub"])
	assert.Equal(t, "testuser", parsed["username"])

	_, err = middleware.ValidateJWT("invalid-token", secret)
	assert.Error(t, err)

	_, err = middleware.ValidateJWT(token, "wrong-secret")
	assert.Error(t, err)
}

func TestAuthenticateToken_EmptyToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	_, err = authenticateToken("", db, "test-secret")
	assert.Error(t, err)
}

func TestAuthenticateToken_ValidToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	userID := uuid.New().String()
	user := models.User{
		ID:        userID,
		Username:  "testuser",
		Password:  "hashedpassword",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err = db.CreateUser(user)
	require.NoError(t, err)

	sessionID := uuid.New().String()

	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
	err = db.CreateSession(session)
	require.NoError(t, err)

	claims := map[string]interface{}{
		"sub":      userID,
		"username": "testuser",
		"jti":      sessionID,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token, err := generateJWTForTest(claims, "test-secret-key-32-chars-long!!")
	require.NoError(t, err)

	resultUserID, err := authenticateToken(token, db, "test-secret-key-32-chars-long!!")
	assert.NoError(t, err)
	assert.Equal(t, userID, resultUserID)
}

func TestAuthenticateToken_InvalidToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	_, err = authenticateToken("not-a-valid-token", db, "test-secret-key-32-chars-long!!")
	assert.Error(t, err)
}

func generateJWTForTest(claims map[string]interface{}, secret string) (string, error) {
	return generateJWT(
		claims["sub"].(string),
		claims["username"].(string),
		claims["jti"].(string),
		secret,
	)
}

func originReq(origin, host string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws/events", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestUpgraderCheckOrigin(t *testing.T) {
	cases := []struct {
		name         string
		corsOrigins  string
		authDisabled bool
		origin       string
		host         string
		want         bool
	}{
		{"no origin header is allowed", "", false, "", "localhost:5001", true},
		{"same-origin allowed when no allowlist", "", false, "http://localhost:5001", "localhost:5001", true},
		{"cross-origin denied when auth on and no allowlist", "", false, "http://localhost:3001", "localhost:5001", false},
		{"dev proxy origin allowed when auth disabled", "", true, "http://localhost:3001", "localhost:5001", true},
		{"127.0.0.1 dev origin allowed when auth disabled", "", true, "http://127.0.0.1:3001", "localhost:5001", true},
		{"non-loopback denied even when auth disabled", "", true, "http://evil.example.com", "localhost:5001", false},
		{"allowlisted origin permitted", "https://capstan.ctsvps.work", false, "https://capstan.ctsvps.work", "capstan.ctsvps.work", true},
		{"non-allowlisted origin denied", "https://capstan.ctsvps.work", false, "https://evil.example.com", "capstan.ctsvps.work", false},
		{"allowlist plus auth disabled still allows loopback", "https://capstan.ctsvps.work", true, "http://localhost:3001", "localhost:5001", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			InitUpgrader(tc.corsOrigins, tc.authDisabled)
			got := upgrader.CheckOrigin(originReq(tc.origin, tc.host))
			assert.Equal(t, tc.want, got)
		})
	}
}
