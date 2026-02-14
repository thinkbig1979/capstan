package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func setupTestRouter(handler *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
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

var testTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
