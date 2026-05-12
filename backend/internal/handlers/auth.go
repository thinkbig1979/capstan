package handlers

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/middleware"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var bearerPrefixRegex = regexp.MustCompile(`^Bearer\s+`)

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

// RegisterPublicRoutes registers public bootstrap endpoints that must not be
// subject to brute-force rate limiting. The frontend polls /status on every
// navigation, so it cannot share a bucket with /login.
func (h *AuthHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/status", h.Status)
}

func (h *AuthHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/setup", h.Setup)
	group.POST("/login", h.Login)
}

func (h *AuthHandler) RegisterProtectedRoutes(group *gin.RouterGroup) {
	group.POST("/auth/logout", h.Logout)
	group.GET("/auth/me", h.Me)
}

func (h *AuthHandler) Status(c *gin.Context) {
	userCount, _ := h.db.UserCount()
	needsSetup := userCount == 0

	if h.authDisabled {
		slog.Warn("AUTHENTICATION DISABLED - Only safe on trusted networks!")
	}

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

	csrfToken := middleware.GenerateCSRFToken()
	setAuthCookies(c, token, csrfToken)

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
		slog.Warn("Failed authentication attempt",
			"ip", c.ClientIP(),
			"username", req.Username,
			"timestamp", time.Now(),
			"reason", "user_not_found")
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
			"Invalid credentials",
		))
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
	if err != nil {
		slog.Warn("Failed authentication attempt",
			"ip", c.ClientIP(),
			"username", req.Username,
			"timestamp", time.Now(),
			"reason", "invalid_password")
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

	csrfToken := middleware.GenerateCSRFToken()
	setAuthCookies(c, token, csrfToken)

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
	token = bearerPrefixRegex.ReplaceAllString(token, "")

	claims, err := parseJWT(token, h.jwtSecret)
	if err == nil {
		if jti, ok := claims["jti"].(string); ok {
			h.db.DeleteSession(jti)
		}
	}

	slog.Info("User logged out", "userID", userID)

	clearAuthCookies(c)

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
	if !middleware.ValidateUsername(username) {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Username must be between 3 and 50 characters and contain only letters, numbers, underscores, and hyphens")
	}
	return nil
}

func validatePassword(password string) *models.AppError {
	if valid, msg := middleware.ValidatePassword(password); !valid {
		return models.NewAppError(http.StatusBadRequest, models.ErrValidation, msg)
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

func setAuthCookies(c *gin.Context, token string, csrfToken string) {
	secure := !strings.Contains(c.Request.Host, "localhost") && !strings.Contains(c.Request.Host, "127.0.0.1")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "docker_manager_token",
		Value:    token,
		MaxAge:   86400,
		Path:     "/",
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "docker_manager_csrf",
		Value:    csrfToken,
		MaxAge:   86400,
		Path:     "/",
		Secure:   secure,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
	c.Header("X-CSRF-Token", csrfToken)
}

func clearAuthCookies(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "docker_manager_token",
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "docker_manager_csrf",
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}
