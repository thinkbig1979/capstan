package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
			models.ErrUnauthorized,
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

	// Reaching here means a valid session pointing at a user row that no longer
	// exists (deleted admin, DB restored from an older snapshot). That session
	// can never resolve a user, so it is session loss, not a bad credential.
	user, err := h.db.GetUserByID(userID.(string))
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrSessionExpired,
			"User not found",
		))
		return
	}

	// The wrong-current-password 401 keeps ErrUnauthorized: the session is
	// still valid and the user must stay on this page to retype (agent-os-318).
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.CurrentPassword))
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewAppError(
			http.StatusUnauthorized,
			models.ErrUnauthorized,
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

	// The validated session id is published on the context by AuthMiddleware
	// (middleware/auth.go:211). Read it here rather than re-parsing the
	// Authorization header, which the browser never sends (App.tsx:58-63
	// registers `() => null` as getToken), so header-only derivation left this
	// empty for every real UI password change and skipped revocation (agent-os-xdn).
	currentSessionID := c.GetString("jti")

	// Keep this guard: DeleteSessionsByUserExcluding(userID, "") matches id != ""
	// and would delete EVERY session for the user, including the caller's own.
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

	// Same second factor as the per-stack .env surface: without a live unlock
	// token, secret-looking values are blanked. Global env routinely holds the
	// credentials every stack shares, so leaving it ungated would just move the
	// hole one endpoint sideways.
	locked := !envUnlocked(c)
	if locked {
		for i := range envVars {
			if isSensitiveEnvKey(envVars[i]["key"]) {
				envVars[i]["value"] = ""
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"vars":   envVars,
		"locked": locked,
	})
}

func (h *SettingsHandler) UpdateGlobalEnv(c *gin.Context) {
	// Gated for the same reason as EnvHandler.Put: a locked session was handed
	// blanked sensitive values, and this endpoint replaces the whole file, so
	// saving from that state would wipe every secret it could not see.
	if !envUnlocked(c) {
		c.JSON(http.StatusForbidden, models.NewAppError(
			http.StatusForbidden,
			models.ErrForbidden,
			"Re-enter your password to edit global environment variables",
		))
		return
	}

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

// applyModeImmediate and applyModeScheduled are the only two accepted values of
// the update_apply_mode setting. Immediate is the default seeded by migration 14.
const (
	applyModeImmediate = "immediate"
	applyModeScheduled = "scheduled"
)

// defaultApplyTime and defaultApplyDays mirror what migration 14 seeds. They are
// the fallback for a settings row that is missing entirely, so that a GET on a
// database predating the migration still renders a usable form.
const (
	defaultApplyTime = "03:00"
	defaultApplyDays = "0,1,2,3,4,5,6"
)

func (h *SettingsHandler) GetUpdateSettings(c *gin.Context) {
	scanIntervalStr, _ := h.db.GetSetting("update_scan_interval")
	lastScanAt, _ := h.db.GetSetting("update_scan_last_run")
	lastScanError, _ := h.db.GetSetting("update_scan_last_error")
	autoUpdateEnabledStr, _ := h.db.GetSetting("auto_update_enabled")
	applyMode, _ := h.db.GetSetting("update_apply_mode")
	applyTime, _ := h.db.GetSetting("update_apply_time")
	applyDaysStr, _ := h.db.GetSetting("update_apply_days")

	if applyMode != applyModeScheduled {
		applyMode = applyModeImmediate
	}
	if applyTime == "" {
		applyTime = defaultApplyTime
	}
	if applyDaysStr == "" {
		applyDaysStr = defaultApplyDays
	}

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

	// ApplyDays is initialised before it is filled so that it marshals as [] and
	// never as null, including on the parse-failure path below.
	response := models.UpdateSettingsResponse{
		ScanIntervalMinutes: scanInterval,
		GlobalAutoUpdate:    autoUpdateEnabledStr == "true",
		ApplyMode:           applyMode,
		ApplyTime:           applyTime,
		ApplyDays:           []int{},
	}
	response.ServerTimezone, response.ServerTimeOffset = services.ServerTimezone()

	weekdays, err := services.ParseWeekdays(applyDaysStr)
	if err != nil {
		// Report the stored days as empty rather than inventing a default: an
		// operator seeing no days selected can see something is wrong, where a
		// silently substituted default would hide it.
		slog.Error("Stored update_apply_days is invalid", "value", applyDaysStr, "error", err)
	}
	for _, day := range weekdays {
		response.ApplyDays = append(response.ApplyDays, int(day))
	}

	// nextApplyAt only means something when an apply is actually going to
	// happen: scheduled mode, auto-update on, and a scan interval that keeps the
	// scheduler (and with it the apply timer) running.
	if applyMode == applyModeScheduled && response.GlobalAutoUpdate && scanInterval > 0 {
		if schedule, schedErr := services.ParseDailySchedule(applyTime, applyDaysStr); schedErr == nil {
			if next, ok := schedule.NextAfter(time.Now()); ok {
				response.NextApplyAt = next.Format(time.RFC3339)
			}
		}
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
	// Pointer fields: an absent key means "leave unchanged". Non-pointer fields
	// bound to their zero value here, so a partial PUT silently wrote interval 0
	// and auto-update false, then stopped the scheduler (agent-os-mtbo.8).
	var req struct {
		ScanIntervalMinutes *int    `json:"scanIntervalMinutes"`
		GlobalAutoUpdate    *bool   `json:"globalAutoUpdate"`
		ApplyMode           *string `json:"applyMode"`
		ApplyTime           *string `json:"applyTime"`
		ApplyDays           *[]int  `json:"applyDays"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Invalid request body",
		))
		return
	}

	if req.ScanIntervalMinutes != nil && *req.ScanIntervalMinutes != 0 && *req.ScanIntervalMinutes < 15 {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			"Scan interval must be 0 (disabled) or at least 15 minutes",
		))
		return
	}

	// Validate the apply schedule before writing anything, so a bad day list
	// cannot leave a half-applied time behind. The service falls back to
	// immediate mode on an unparseable stored schedule; this is the other half
	// of that pair — reject it at the door so it never gets stored.
	if req.ApplyMode != nil && *req.ApplyMode != applyModeImmediate && *req.ApplyMode != applyModeScheduled {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"VALIDATION_ERROR",
			fmt.Sprintf("Invalid apply mode %q: expected %q or %q", *req.ApplyMode, applyModeImmediate, applyModeScheduled),
		))
		return
	}
	if req.ApplyTime != nil {
		if _, _, err := services.ParseScheduleTime(*req.ApplyTime); err != nil {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				err.Error(),
			))
			return
		}
	}
	applyDaysCSV := ""
	if req.ApplyDays != nil {
		parts := make([]string, 0, len(*req.ApplyDays))
		for _, day := range *req.ApplyDays {
			parts = append(parts, strconv.Itoa(day))
		}
		weekdays, err := services.ParseWeekdays(strings.Join(parts, ","))
		if err != nil {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				err.Error(),
			))
			return
		}
		// Store the normalised (sorted, deduped) form so a GET round-trips.
		applyDaysCSV = services.FormatWeekdays(weekdays)
	}

	applied := gin.H{"setting": "update_schedule"}

	if req.ScanIntervalMinutes != nil {
		oldIntervalStr, _ := h.db.GetSetting("update_scan_interval")
		oldInterval := 0
		if oldIntervalStr != "" {
			if v, err := strconv.Atoi(oldIntervalStr); err == nil {
				oldInterval = v
			}
		}

		if err := h.db.SetSetting("update_scan_interval", fmt.Sprintf("%d", *req.ScanIntervalMinutes)); err != nil {
			slog.Error("Failed to update scan interval", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update scan interval",
			))
			return
		}
		applied["scan_interval"] = *req.ScanIntervalMinutes

		if h.scheduler != nil && *req.ScanIntervalMinutes != oldInterval {
			if *req.ScanIntervalMinutes > 0 {
				h.scheduler.Restart(time.Duration(*req.ScanIntervalMinutes) * time.Minute)
			} else {
				h.scheduler.Stop()
			}
		}
	}

	if req.GlobalAutoUpdate != nil {
		autoUpdateVal := "false"
		if *req.GlobalAutoUpdate {
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
		applied["auto_update"] = *req.GlobalAutoUpdate
	}

	// Every audit key below is set INSIDE its own nil guard, never above it:
	// an "applied" entry for a field the request never sent would make the audit
	// row tell exactly the lie the all-pointer conversion (agent-os-mtbo.8) was
	// written to stop telling.
	if req.ApplyMode != nil {
		if err := h.db.SetSetting("update_apply_mode", *req.ApplyMode); err != nil {
			slog.Error("Failed to update apply mode", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update apply mode",
			))
			return
		}
		applied["apply_mode"] = *req.ApplyMode
	}

	if req.ApplyTime != nil {
		if err := h.db.SetSetting("update_apply_time", *req.ApplyTime); err != nil {
			slog.Error("Failed to update apply time", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update apply time",
			))
			return
		}
		applied["apply_time"] = *req.ApplyTime
	}

	if req.ApplyDays != nil {
		if err := h.db.SetSetting("update_apply_days", applyDaysCSV); err != nil {
			slog.Error("Failed to update apply days", "error", err)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Failed to update apply days",
			))
			return
		}
		applied["apply_days"] = applyDaysCSV
	}

	// Re-arm on ANY of the three, not just on a changed interval: a schedule
	// edit that leaves the interval alone still has to reach the running apply
	// timer, or it takes effect only at the next restart.
	//
	// This is deliberately NOT h.scheduler.Restart(): the merged settings screen
	// sends applyMode/applyTime/applyDays on their own, and stopping the scan
	// scheduler on a pure schedule edit would be the mtbo.8 bug in a new place.
	if h.scheduler != nil && (req.ApplyMode != nil || req.ApplyTime != nil || req.ApplyDays != nil) {
		h.scheduler.ReloadApplySchedule()
	}

	slog.Info("Update settings changed", "applied", applied)
	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateSettings, applied)

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

	// hasHttpsToken must distinguish "no row" (never configured) from "row
	// present but unreadable" (a decrypt failure, e.g. after a STORAGE_KEY
	// rotation). The old `httpsToken, _ := h.db.GetSetting(...)` discarded
	// that distinction and reported hasHttpsToken=false in both cases —
	// telling the operator no credential was configured when one was, in
	// fact, stored and merely unreadable (agent-os-oyj). This mirrors the
	// fail-closed fix to httpsCredentials in git_credentials.go: both places
	// now treat "unreadable" as its own state instead of silently folding it
	// into "absent".
	hasToken := false
	httpsTokenUnreadable := false
	httpsToken, err := h.db.GetSetting("git_https_token")
	switch {
	case err == nil:
		hasToken = httpsToken != "" || h.cfg.GitHTTPSToken != ""
	case errors.Is(err, sql.ErrNoRows):
		// The healthy "never configured" state — GIT_HTTPS_TOKEN is still a
		// legitimate source of a credential, so report based on that alone.
		hasToken = h.cfg.GitHTTPSToken != ""
	default:
		// A row exists but could not be decrypted. A credential IS configured
		// from the operator's point of view — it just can't be read right
		// now — so hasHttpsToken stays true rather than reporting "not
		// configured". httpsTokenUnreadable is additive (the frontend ignores
		// unknown JSON keys) and gives the UI an honest reason to prompt for
		// the token to be re-entered.
		hasToken = true
		httpsTokenUnreadable = true
	}

	c.JSON(http.StatusOK, gin.H{
		"sshKey":               sshKey,
		"httpsUser":            httpsUser,
		"hasHttpsToken":        hasToken,
		"httpsTokenUnreadable": httpsTokenUnreadable,
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
