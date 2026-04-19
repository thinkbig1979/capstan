package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/middleware"
	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type SettingsHandler struct {
	db           *database.DB
	stacksDir    string
	jwtSecret    string
	authDisabled bool
}

func NewSettingsHandler(db *database.DB, stacksDir string, jwtSecret string, authDisabled bool) *SettingsHandler {
	return &SettingsHandler{
		db:           db,
		stacksDir:    stacksDir,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
	}
}

func (h *SettingsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.PUT("/auth/password", h.ChangePassword)
	group.GET("/settings/config", h.GetConfig)
	group.GET("/settings/global-env", h.GetGlobalEnv)
	group.PUT("/settings/global-env", h.UpdateGlobalEnv)
	group.GET("/settings/log-retention", h.GetLogRetention)
	group.PUT("/settings/log-retention", h.UpdateLogRetention)
}

func (h *SettingsHandler) GetConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"stacksDir": h.stacksDir,
	})
}

func (h *SettingsHandler) ChangePassword(c *gin.Context) {
	if h.authDisabled {
		c.JSON(http.StatusForbidden, models.NewAppError(
			http.StatusForbidden,
			"FORBIDDEN",
			"Cannot change password when auth is disabled",
		))
		return
	}

	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			"UNAUTHORIZED",
			"Not authenticated",
		))
		return
	}

	var req struct {
		CurrentPassword string `json:"currentPassword" binding:"required"`
		NewPassword     string `json:"newPassword" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	if valid, msg := middleware.ValidatePassword(req.NewPassword); !valid {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			msg,
		))
		return
	}

	user, err := h.db.GetUserByID(userID.(string))
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			"UNAUTHORIZED",
			"User not found",
		))
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.CurrentPassword))
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			"UNAUTHORIZED",
			"Current password is incorrect",
		))
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to hash password",
		))
		return
	}

	user.Password = string(hashedPassword)
	user.UpdatedAt = time.Now()

	if err := h.db.UpdateUserPassword(user.ID, string(hashedPassword), time.Now()); err != nil {
		slog.Error("Failed to update password", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to update password",
		))
		return
	}

	verifiedUser, err := h.db.GetUserByID(user.ID)
	if err != nil || verifiedUser == nil {
		slog.Error("Failed to verify password update", "error", err, "userID", userID)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to verify password update",
		))
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(verifiedUser.Password), []byte(req.NewPassword))
	if err != nil {
		slog.Error("Password verification failed after update", "error", err, "userID", userID)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Password verification failed",
		))
		return
	}

	slog.Info("Password changed", "userID", userID)
	c.Status(http.StatusNoContent)
}

func (h *SettingsHandler) GetGlobalEnv(c *gin.Context) {
	globalEnvPath := h.stacksDir + "/global.env"

	envVars, err := parseEnvFile(globalEnvPath)
	if err != nil {
		if !strings.Contains(err.Error(), "no such file") {
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to read global environment file",
			))
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"vars": envVars,
	})
}

func (h *SettingsHandler) UpdateGlobalEnv(c *gin.Context) {
	var req struct {
		Vars []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"vars"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	for _, v := range req.Vars {
		if v.Key == "" {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				"Environment variable keys cannot be empty",
			))
			return
		}
		if strings.Contains(v.Key, "\n") || strings.Contains(v.Value, "\n") {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				"Environment variables cannot contain newlines",
			))
			return
		}
	}

	globalEnvPath := h.stacksDir + "/global.env"
	content := ""
	for _, v := range req.Vars {
		if v.Key != "" && v.Value != "" {
			content += v.Key + "=" + v.Value + "\n"
		}
	}

	if err := os.WriteFile(globalEnvPath, []byte(content), 0600); err != nil {
		slog.Error("Failed to write global environment file", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to write global environment file",
		))
		return
	}

	slog.Info("Global environment updated")
	c.Status(http.StatusNoContent)
}

func parseEnvFile(path string) ([]map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]string{}, nil
	}

	lines := strings.Split(string(data), "\n")
	vars := []map[string]string{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			vars = append(vars, map[string]string{
				"key":   key,
				"value": value,
			})
		}
	}

	return vars, nil
}

func (h *SettingsHandler) GetLogRetention(c *gin.Context) {
	retentionStr, err := h.db.GetSetting("max_log_retention_days")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to get log retention setting",
		))
		return
	}

	retentionDays := 90
	if retentionStr != "" {
		if _, err := fmt.Sscanf(retentionStr, "%d", &retentionDays); err != nil {
			slog.Error("Failed to parse log retention days", "error", err)
			retentionDays = 90
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"retentionDays": retentionDays,
	})
}

func (h *SettingsHandler) UpdateLogRetention(c *gin.Context) {
	var req struct {
		RetentionDays int `json:"retentionDays" binding:"required,min=7"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Retention days must be at least 7",
		))
		return
	}

	if err := h.db.SetSetting("max_log_retention_days", fmt.Sprintf("%d", req.RetentionDays)); err != nil {
		slog.Error("Failed to update log retention setting", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to update log retention setting",
		))
		return
	}

	slog.Info("Log retention updated", "retention_days", req.RetentionDays)
	c.Status(http.StatusNoContent)
}
