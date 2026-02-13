package handlers

import (
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	db           *database.DB
	jwtSecret    string
	authDisabled bool
}

func NewAuthHandler(db *database.DB, jwtSecret string, authDisabled bool) *AuthHandler {
	return &AuthHandler{
		db:           db,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
	}
}

func (h *AuthHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/status", h.Status)
	group.POST("/setup", h.Setup)
	group.POST("/login", h.Login)
	group.POST("/logout", h.Logout)
	group.GET("/me", h.Me)
}

func (h *AuthHandler) Status(c *gin.Context) {
	userCount, _ := h.db.UserCount()
	needsSetup := userCount == 0

	c.JSON(http.StatusOK, gin.H{
		"needsSetup":   needsSetup,
		"authDisabled": h.authDisabled,
	})
}

func (h *AuthHandler) Setup(c *gin.Context) {
	userCount, _ := h.db.UserCount()
	if userCount > 0 {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrSetupAlreadyDone,
			"Setup has already been completed",
		))
		return
	}

	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if err := validateUsername(req.Username); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}

	if err := validatePassword(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, err)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to hash password",
		))
		return
	}

	userID := uuid.New().String()
	now := time.Now()

	user := models.User{
		ID:        userID,
		Username:  req.Username,
		Password:  string(hashedPassword),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.db.CreateUser(user); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to create user",
		))
		return
	}

	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour)

	session := models.Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}

	if err := h.db.CreateSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to create session",
		))
		return
	}

	token, err := generateJWT(userID, req.Username, sessionID, h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to generate token",
		))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":       userID,
			"username": req.Username,
		},
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	user, err := h.db.GetUserByUsername(req.Username)
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Invalid credentials",
		))
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Invalid credentials",
		))
		return
	}

	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(24 * time.Hour)
	now := time.Now()

	session := models.Session{
		ID:        sessionID,
		UserID:    user.ID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}

	if err := h.db.CreateSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to create session",
		))
		return
	}

	token, err := generateJWT(user.ID, user.Username, sessionID, h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to generate token",
		))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
		},
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Not authenticated",
		))
		return
	}

	token := c.GetHeader("Authorization")
	token = regexp.MustCompile(`^Bearer\s+`).ReplaceAllString(token, "")

	claims, err := parseJWT(token, h.jwtSecret)
	if err == nil {
		if jti, ok := claims["jti"].(string); ok {
			h.db.DeleteSession(jti)
		}
	}

	slog.Info("User logged out", "userID", userID)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Not authenticated",
		))
		return
	}

	user, err := h.db.GetUserByID(userID.(string))
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"User not found",
		))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":       user.ID,
		"username": user.Username,
	})
}

func validateUsername(username string) *models.AppError {
	if len(username) < 3 || len(username) > 50 {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Username must be between 3 and 50 characters")
	}

	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, username)
	if !matched {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Username can only contain letters, numbers, underscores, and hyphens")
	}

	return nil
}

func validatePassword(password string) *models.AppError {
	if len(password) < 8 || len(password) > 128 {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Password must be between 8 and 128 characters")
	}

	return nil
}

func generateJWT(userID, username, sessionID, secret string) (string, error) {
	claims := jwtv5.MapClaims{
		"sub":      userID,
		"username": username,
		"jti":      sessionID,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func parseJWT(token, secret string) (jwtv5.MapClaims, error) {
	parsedToken, err := jwtv5.Parse(token, func(token *jwtv5.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtv5.SigningMethodHMAC); !ok {
			return nil, jwtv5.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := parsedToken.Claims.(jwtv5.MapClaims); ok && parsedToken.Valid {
		return claims, nil
	}

	return nil, jwtv5.ErrInvalidKey
}
