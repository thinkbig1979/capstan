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

	"github.com/gin-gonic/gin"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"golang.org/x/crypto/bcrypt"
)

type SettingsHandler struct {
	db           *database.DB
	stacksDir    string
	jwtSecret    string
	authDisabled bool
	scheduler    *services.SchedulerService
	cfg          *config.Config
	actionLog    *services.ActionLogger
}

func NewSettingsHandler(db *database.DB, stacksDir string, jwtSecret string, authDisabled bool, scheduler *services.SchedulerService, cfg *config.Config) *SettingsHandler {
	return &SettingsHandler{
		db:           db,
		stacksDir:    stacksDir,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
		scheduler:    scheduler,
		cfg:          cfg,
		actionLog:    services.NewActionLogger(db),
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
	logActionFromContext(h.actionLog, c, nil, services.ActionChangePassword, gin.H{})

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

	entries := make([]EnvEntry, len(req.Vars))
	for i, v := range req.Vars {
		entries[i] = EnvEntry{Key: v.Key, Value: v.Value}
	}

	// Reuse the stack-env validation rules: reject an empty key paired with a
	// non-empty value (would serialise as a corrupt "=value" line) and reject
	// newlines/CRs in keys or values (finding #15 / B4). An entry with both an
	// empty key and empty value is treated as a no-op blank line, matching
	// serializeEnvFile below, rather than rejected outright.
	if err := validateEnvEntries(entries); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			err.Error(),
		))
		return
	}

	globalEnvPath := h.cfg.DataDir + "/global.env"
	// serializeEnvFile only skips an entry when BOTH key and value are empty,
	// so a deliberately empty value (KEY=) round-trips instead of being
	// silently dropped.
	content := serializeEnvFile(entries)

	if err := writeEnvFileAtomic(globalEnvPath, content); err != nil {
		slog.Error("Failed to write global environment file", "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to write global environment file",
		))
		return
	}

	slog.Info("Global environment updated")
	// Audit the change but never the values themselves — global env routinely
	// holds secrets. Record only how many variables are now defined.
	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateGlobalEnv, gin.H{"count": len(req.Vars)})
	c.Status(http.StatusNoContent)
}

func parseEnvFile(path string) ([]map[string]string, error) {
	//nolint:gosec // path's only caller passes h.cfg.DataDir + "/global.env", config-derived, never request input
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
	// All three histories are read through the same clamped accessor, so the
	// UI shows the value that will actually be applied rather than the raw row.
	c.JSON(http.StatusOK, gin.H{
		"retentionDays":              h.db.RetentionDays(database.SettingLogRetentionDays),
		"updateHistoryRetentionDays": h.db.RetentionDays(database.SettingUpdateHistoryRetentionDays),
		"backupHistoryRetentionDays": h.db.RetentionDays(database.SettingBackupHistoryRetentionDays),
		"minRetentionDays":           database.MinRetentionDays,
	})
}

func (h *SettingsHandler) UpdateLogRetention(c *gin.Context) {
	// All three fields are optional so a client can update one without having
	// to know the others; at least one must be present.
	var req struct {
		RetentionDays              *int `json:"retentionDays"`
		UpdateHistoryRetentionDays *int `json:"updateHistoryRetentionDays"`
		BackupHistoryRetentionDays *int `json:"backupHistoryRetentionDays"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	updates := []struct {
		value *int
		key   string
		label string
	}{
		{req.RetentionDays, database.SettingLogRetentionDays, "log_retention"},
		{req.UpdateHistoryRetentionDays, database.SettingUpdateHistoryRetentionDays, "update_history_retention"},
		{req.BackupHistoryRetentionDays, database.SettingBackupHistoryRetentionDays, "backup_history_retention"},
	}

	applied := gin.H{}
	for _, u := range updates {
		if u.value == nil {
			continue
		}
		if *u.value < database.MinRetentionDays {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				fmt.Sprintf("Retention days must be at least %d", database.MinRetentionDays),
			))
			return
		}
		if err := h.db.SetSetting(u.key, strconv.Itoa(*u.value)); err != nil {
			slog.Error("Failed to update retention setting", "setting", u.label, "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update retention setting",
			))
			return
		}
		applied[u.label] = *u.value
	}

	if len(applied) == 0 {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"At least one retention value is required",
		))
		return
	}

	slog.Info("Retention settings updated", "applied", applied)
	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateSettings, gin.H{
		"setting": "retention",
		"applied": applied,
	})
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
	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateSettings, gin.H{
		"setting":       "update_schedule",
		"scan_interval": req.ScanIntervalMinutes,
		"auto_update":   req.GlobalAutoUpdate,
	})

	h.GetUpdateSettings(c)
}

// looksLikePrivateKey reports whether s appears to be private-key material
// rather than a filesystem path. SSH key file paths are single-line and never
// contain a PEM/OpenSSH key header.
func looksLikePrivateKey(s string) bool {
	return strings.Contains(s, "-----BEGIN") ||
		strings.Contains(s, "PRIVATE KEY") ||
		strings.ContainsAny(s, "\n\r")
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
		// git_ssh_key is a path to a key file, not key material. Reject pasted
		// private keys so private-key bytes are never stored in (or echoed back
		// from) the settings store (M4).
		if looksLikePrivateKey(req.SSHKey) {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				"git SSH key must be a path to a key file, not the key contents",
			))
			return
		}
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
			if respondIfEncryptionUnavailable(c, err) {
				return
			}
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
	// Record which credential fields were changed, never their values.
	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateGitSettings, gin.H{
		"ssh_key":     req.SSHKey != "",
		"https_user":  req.HTTPSUser != "",
		"https_token": req.HTTPSToken != "",
	})
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
		logActionFromContext(h.actionLog, c, nil, services.ActionUpdateSettings, gin.H{
			"setting": "default_directory",
			"path":    absDir,
		})
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

	filter := database.ActionLogFilter{
		Action:   c.Query("action"),
		Search:   c.Query("search"),
		DateFrom: c.Query("dateFrom"),
		DateTo:   c.Query("dateTo"),
	}

	actions, total, err := h.db.ListActionLogsFiltered(pageSize, offset, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to retrieve audit log",
		))
		return
	}

	availableActions, err := h.db.DistinctActionLogActions()
	if err != nil {
		availableActions = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"entries":          actions,
		"total":            total,
		"page":             page,
		"pageSize":         pageSize,
		"availableActions": availableActions,
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
	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateSettings, gin.H{
		"setting":    "scan_depth",
		"scan_depth": req.ScanDepth,
	})
	h.GetScanDepth(c)
}
