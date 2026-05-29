package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/gin-gonic/gin"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type SettingsHandler struct {
	db           *database.DB
	stacksDir    string
	jwtSecret    string
	authDisabled bool
	scheduler    *services.SchedulerService
	cfg          *config.Config
}

func NewSettingsHandler(db *database.DB, stacksDir string, jwtSecret string, authDisabled bool, scheduler *services.SchedulerService, cfg *config.Config) *SettingsHandler {
	return &SettingsHandler{
		db:           db,
		stacksDir:    stacksDir,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
		scheduler:    scheduler,
		cfg:          cfg,
	}
}

func (h *SettingsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.PUT("/auth/password", h.ChangePassword)
	group.GET("/settings/config", h.GetConfig)
	group.GET("/settings/global-env", h.GetGlobalEnv)
	group.PUT("/settings/global-env", h.UpdateGlobalEnv)
	group.GET("/settings/log-retention", h.GetLogRetention)
	group.PUT("/settings/log-retention", h.UpdateLogRetention)
	group.GET("/settings/updates", h.GetUpdateSettings)
	group.PUT("/settings/updates", h.UpdateUpdateSettings)
	group.GET("/settings/git", h.GetGitSettings)
	group.PUT("/settings/git", h.UpdateGitSettings)
	group.GET("/settings/directories", h.GetConfiguredDirectories)
	group.PUT("/settings/directories", h.UpdateConfiguredDirectories)
	group.GET("/settings/scan-depth", h.GetScanDepth)
	group.PUT("/settings/scan-depth", h.UpdateScanDepth)
	group.GET("/settings/audit-log", h.GetAuditLog)
}

func (h *SettingsHandler) GetConfig(c *gin.Context) {
	allDirs := h.cfg.GetAllStacksDirs()
	c.JSON(http.StatusOK, gin.H{
		"stacksDir":         h.stacksDir,
		"stacksDirectories": allDirs,
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

	currentSessionID := ""
	authToken := c.GetHeader("Authorization")
	if authToken != "" {
		tokenStr := strings.TrimPrefix(authToken, "Bearer ")
		if parsedToken, err := jwtv5.Parse(tokenStr, func(token *jwtv5.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwtv5.SigningMethodHMAC); !ok {
				return nil, jwtv5.ErrSignatureInvalid
			}
			return []byte(h.jwtSecret), nil
		}); err == nil {
			if claims, ok := parsedToken.Claims.(jwtv5.MapClaims); ok {
				if jti, ok := claims["jti"].(string); ok {
					currentSessionID = jti
				}
			}
		}
	}

	if currentSessionID != "" {
		if err := h.db.DeleteSessionsByUserExcluding(userID.(string), currentSessionID); err != nil {
			slog.Error("Failed to invalidate other sessions after password change", "error", err, "userID", userID)
		}
	}

	c.Status(http.StatusNoContent)
}

func (h *SettingsHandler) GetGlobalEnv(c *gin.Context) {
	globalEnvPath := h.cfg.DataDir + "/global.env"

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

	globalEnvPath := h.cfg.DataDir + "/global.env"
	var sb strings.Builder
	for _, v := range req.Vars {
		if v.Key != "" && v.Value != "" {
			sb.WriteString(v.Key)
			sb.WriteByte('=')
			sb.WriteString(v.Value)
			sb.WriteByte('\n')
		}
	}

	if err := os.WriteFile(globalEnvPath, []byte(sb.String()), 0600); err != nil {
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

func (h *SettingsHandler) GetUpdateSettings(c *gin.Context) {
	scanIntervalStr, _ := h.db.GetSetting("update_scan_interval")
	lastScanAt, _ := h.db.GetSetting("update_scan_last_run")
	lastScanError, _ := h.db.GetSetting("update_scan_last_error")
	autoUpdateEnabledStr, _ := h.db.GetSetting("auto_update_enabled")

	scanInterval := 0
	if scanIntervalStr != "" {
		if v, err := strconv.Atoi(scanIntervalStr); err == nil {
			scanInterval = v
		}
	}

	enabledContainers, last7Days, last30Days, err := h.db.GetUpdateStats()
	if err != nil {
		slog.Error("Failed to get update stats", "error", err)
	}

	var lastScanAtPtr *string
	if lastScanAt != "" {
		lastScanAtPtr = &lastScanAt
	}
	var lastScanErrorPtr *string
	if lastScanError != "" {
		lastScanErrorPtr = &lastScanError
	}

	response := models.UpdateSettingsResponse{
		ScanIntervalMinutes: scanInterval,
		GlobalAutoUpdate:    autoUpdateEnabledStr == "true",
	}
	if lastScanAtPtr != nil {
		response.LastScanAt = *lastScanAtPtr
	}
	if lastScanErrorPtr != nil {
		response.LastScanError = *lastScanErrorPtr
	}
	response.AutoUpdateStats.EnabledContainers = enabledContainers
	response.AutoUpdateStats.UpdatesLast7Days = last7Days
	response.AutoUpdateStats.UpdatesLast30Days = last30Days

	c.JSON(http.StatusOK, response)
}

func (h *SettingsHandler) UpdateUpdateSettings(c *gin.Context) {
	var req struct {
		ScanIntervalMinutes int  `json:"scanIntervalMinutes"`
		GlobalAutoUpdate    bool `json:"globalAutoUpdate"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	if req.ScanIntervalMinutes != 0 && req.ScanIntervalMinutes < 15 {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Scan interval must be 0 (disabled) or at least 15 minutes",
		))
		return
	}

	oldIntervalStr, _ := h.db.GetSetting("update_scan_interval")
	oldInterval := 0
	if oldIntervalStr != "" {
		if v, err := strconv.Atoi(oldIntervalStr); err == nil {
			oldInterval = v
		}
	}

	if err := h.db.SetSetting("update_scan_interval", fmt.Sprintf("%d", req.ScanIntervalMinutes)); err != nil {
		slog.Error("Failed to update scan interval", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to update scan interval",
		))
		return
	}

	autoUpdateVal := "false"
	if req.GlobalAutoUpdate {
		autoUpdateVal = "true"
	}
	if err := h.db.SetSetting("auto_update_enabled", autoUpdateVal); err != nil {
		slog.Error("Failed to update auto-update setting", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to update auto-update setting",
		))
		return
	}

	if h.scheduler != nil && req.ScanIntervalMinutes != oldInterval {
		if req.ScanIntervalMinutes > 0 {
			h.scheduler.Restart(time.Duration(req.ScanIntervalMinutes) * time.Minute)
		} else {
			h.scheduler.Stop()
		}
	}

	slog.Info("Update settings changed",
		"scan_interval", req.ScanIntervalMinutes,
		"auto_update", req.GlobalAutoUpdate)

	h.GetUpdateSettings(c)
}

func (h *SettingsHandler) GetGitSettings(c *gin.Context) {
	sshKey, _ := h.db.GetSetting("git_ssh_key")
	if sshKey == "" {
		sshKey = h.cfg.GitSSHKey
	}
	httpsUser, _ := h.db.GetSetting("git_https_user")
	if httpsUser == "" {
		httpsUser = h.cfg.GitHTTPSUser
	}
	hasToken := false
	httpsToken, _ := h.db.GetSetting("git_https_token")
	if httpsToken == "" && h.cfg.GitHTTPSToken != "" {
		hasToken = true
	} else if httpsToken != "" {
		hasToken = true
	}

	c.JSON(http.StatusOK, gin.H{
		"sshKey":        sshKey,
		"httpsUser":     httpsUser,
		"hasHttpsToken": hasToken,
	})
}

func (h *SettingsHandler) UpdateGitSettings(c *gin.Context) {
	var req struct {
		SSHKey     string `json:"sshKey"`
		HTTPSUser  string `json:"httpsUser"`
		HTTPSToken string `json:"httpsToken"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	if req.SSHKey != "" {
		if err := h.db.SetSetting("git_ssh_key", req.SSHKey); err != nil {
			slog.Error("Failed to update git SSH key setting", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update git SSH key",
			))
			return
		}
	}

	if req.HTTPSUser != "" {
		if err := h.db.SetSetting("git_https_user", req.HTTPSUser); err != nil {
			slog.Error("Failed to update git HTTPS user setting", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update git HTTPS user",
			))
			return
		}
	}

	if req.HTTPSToken != "" {
		if err := h.db.SetSetting("git_https_token", req.HTTPSToken); err != nil {
			slog.Error("Failed to update git HTTPS token setting", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update git HTTPS token",
			))
			return
		}
	}

	slog.Info("Git settings updated")
	h.GetGitSettings(c)
}

func (h *SettingsHandler) GetConfiguredDirectories(c *gin.Context) {
	allDirs := h.cfg.GetAllStacksDirs()

	type ConfiguredDir struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
	}

	result := make([]ConfiguredDir, 0, len(allDirs))
	for i, dir := range allDirs {
		name := filepath.Base(dir)
		if dir == "/" {
			name = "root"
		}
		result = append(result, ConfiguredDir{
			Path:      dir,
			Name:      name,
			IsDefault: i == 0,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"directories": result,
		"defaultDir":  allDirs[0],
	})
}

func (h *SettingsHandler) UpdateConfiguredDirectories(c *gin.Context) {
	var req struct {
		DefaultDir string `json:"defaultDir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	if req.DefaultDir != "" {
		absDir, err := filepath.Abs(req.DefaultDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				"Invalid directory path",
			))
			return
		}

		if !h.isPathWithinAllowedDirs(absDir) {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				"Directory must be within a configured stacks directory",
			))
			return
		}

		if err := os.MkdirAll(absDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to create directory",
			))
			return
		}

		if err := h.db.SetSetting("default_stacks_dir", absDir); err != nil {
			slog.Error("Failed to update default stacks dir", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update default stacks directory",
			))
			return
		}

		h.stacksDir = absDir
		h.cfg.StacksDir = absDir
		slog.Info("Default stacks directory updated", "path", absDir)
	}

	h.GetConfiguredDirectories(c)
}

func (h *SettingsHandler) isPathWithinAllowedDirs(path string) bool {
	for _, dir := range h.cfg.GetAllStacksDirs() {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if path == absDir || strings.HasPrefix(path, absDir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (h *SettingsHandler) GetAuditLog(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("pageSize", "50")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}

	offset := (page - 1) * pageSize

	actions, total, err := h.db.ListActionLogsPaginated(pageSize, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to retrieve audit log",
		))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entries":  actions,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

func (h *SettingsHandler) GetScanDepth(c *gin.Context) {
	depthStr, err := h.db.GetSetting("scan_depth")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to get scan depth setting",
		))
		return
	}

	depth := 1
	if depthStr != "" {
		if v, parseErr := strconv.Atoi(depthStr); parseErr == nil && v >= 1 {
			depth = v
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"scanDepth": depth,
	})
}

func (h *SettingsHandler) UpdateScanDepth(c *gin.Context) {
	var req struct {
		ScanDepth int `json:"scanDepth" binding:"required,min=1,max=10"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Scan depth must be between 1 and 10",
		))
		return
	}

	if err := h.db.SetSetting("scan_depth", fmt.Sprintf("%d", req.ScanDepth)); err != nil {
		slog.Error("Failed to update scan depth setting", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to update scan depth setting",
		))
		return
	}

	slog.Info("Scan depth updated", "scan_depth", req.ScanDepth)
	h.GetScanDepth(c)
}
