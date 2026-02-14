package handlers

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		cm.Add(conn.ID, conn)
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

	parsed, err := validateJWT(token, secret)
	assert.NoError(t, err)
	assert.Equal(t, "user123", parsed["sub"])
	assert.Equal(t, "testuser", parsed["username"])

	_, err = validateJWT("invalid-token", secret)
	assert.Error(t, err)

	_, err = validateJWT(token, "wrong-secret")
	assert.Error(t, err)
}

func TestAuthenticateWS_MissingToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.New(tempDir)
	require.NoError(t, err)
	defer db.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/ws", nil)

	_, err = authenticateWS(c, db, "test-secret", false)
	assert.Error(t, err)
}

func TestAuthenticateWS_ValidToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.New(tempDir)
	require.NoError(t, err)
	defer db.Close()

	err = database.RunMigrations(db)
	require.NoError(t, err)

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

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/ws?token="+token, nil)

	resultUserID, err := authenticateWS(c, db, "test-secret-key-32-chars-long!!", false)
	assert.NoError(t, err)
	assert.Equal(t, userID, resultUserID)
}

func TestAuthenticateWS_AuthDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-db-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	db, err := database.New(tempDir)
	require.NoError(t, err)
	defer db.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/ws", nil)

	userID, err := authenticateWS(c, db, "", true)
	assert.NoError(t, err)
	assert.Equal(t, "anonymous", userID)
}

func generateJWTForTest(claims map[string]interface{}, secret string) (string, error) {
	return generateJWT(
		claims["sub"].(string),
		claims["username"].(string),
		claims["jti"].(string),
		secret,
	)
}
