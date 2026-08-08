package handlers

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func setupTestRouter(handler *AuthHandler) *gin.Engine {
	router := gin.New()
	router.GET("/auth/status", handler.Status)
	router.POST("/auth/setup", handler.Setup)
	router.POST("/auth/login", handler.Login)
	router.POST("/auth/logout", handler.Logout)
	router.GET("/auth/me", handler.Me)
	return router
}

func createTestUser(t *testing.T, db *database.DB, username, password string) models.User {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)

	user := models.User{
		ID:        "test-user-id",
		Username:  username,
		Password:  string(hashedPassword),
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}

	err = db.CreateUser(user)
	require.NoError(t, err)

	return user
}

func authHeader(token string) string {
	return "Bearer " + token
}

func createTestDirectory(t *testing.T, db *database.DB, path string) {
	dir := models.Directory{
		Path:      path,
		Name:      filepath.Base(path),
		IsGitRepo: false,
		ScannedAt: time.Now(),
	}
	err := db.UpsertDirectory(dir)
	require.NoError(t, err)
}

func authContextMiddleware(userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("userID", userID)
		c.Next()
	}
}

// envUnlockedMiddleware puts a request in the state a valid X-Unlock-Token would
// (middleware.EnvUnlock), for the tests whose subject is env parsing, saving or
// auditing rather than the unlock gate itself. Tests that exercise the gate build
// the real middleware over a real store instead — see env_unlock_gate_test.go.
func envUnlockedMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.CtxEnvUnlocked, true)
		c.Next()
	}
}

func setupTestRouterWithAuth(handler *AuthHandler, jwtSecret string) *gin.Engine {
	router := gin.New()

	jwtAuth := func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			claims, err := parseJWT(token, jwtSecret)
			if err == nil {
				if sub, ok := claims["sub"].(string); ok {
					c.Set("userID", sub)
				}
			}
		}
		c.Next()
	}

	router.GET("/auth/status", handler.Status)
	router.POST("/auth/setup", handler.Setup)
	router.POST("/auth/login", handler.Login)
	router.POST("/auth/logout", jwtAuth, handler.Logout)
	router.GET("/auth/me", jwtAuth, handler.Me)
	return router
}

var testTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
