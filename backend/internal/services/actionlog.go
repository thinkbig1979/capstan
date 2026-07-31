package services

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

const (
	ActionStart       = "start"
	ActionStop        = "stop"
	ActionRestart     = "restart"
	ActionPull        = "pull"
	ActionDelete      = "delete"
	ActionEditCompose = "edit_compose"
	ActionEditEnv     = "edit_env"
	ActionGitPull     = "git_pull"
	ActionLogin       = "login"
	ActionLoginFailed = "login_failed"
	ActionLogout      = "logout"
	ActionSetup       = "setup"
	ActionScan        = "scan"
	ActionBackup      = "backup"
	ActionRestore     = "restore"

	// Settings changes (see SettingsHandler). Values never carry secrets — only
	// which setting changed and non-sensitive metadata.
	ActionChangePassword    = "change_password"
	ActionUpdateGlobalEnv   = "update_global_env"
	ActionUpdateGitSettings = "update_git_settings"
	ActionUpdateSettings    = "update_settings"

	// Docker resource mutations (see ResourcesHandler).
	ActionDeleteContainer = "delete_container"
	ActionDeleteImage     = "delete_image"
	ActionDeleteVolume    = "delete_volume"
	ActionDeleteNetwork   = "delete_network"
	ActionCreateNetwork   = "create_network"
	ActionPrune           = "prune"
	ActionUpdateContainer = "update_container"
	ActionUpdateStack     = "update_stack"
)

type ActionLogger struct {
	db *database.DB
}

func NewActionLogger(db *database.DB) *ActionLogger {
	return &ActionLogger{db: db}
}

// Log records an action with no request correlation. Background jobs and
// schedulers serve no HTTP request, so they legitimately have no ID.
func (l *ActionLogger) Log(userID string, stackID *string, action string, detail interface{}) {
	l.LogWithRequest("", userID, stackID, action, detail)
}

// LogWithRequest records an action tagged with the ID of the request that
// caused it, so an HTTP log line and the audit row it produced can be joined
// (agent-os-7li).
func (l *ActionLogger) LogWithRequest(requestID, userID string, stackID *string, action string, detail interface{}) {
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		slog.Error("failed to marshal action detail", "action", action, "error", err)
		return
	}

	var stackIDStr string
	if stackID != nil {
		stackIDStr = *stackID
	}

	actionLog := models.ActionLog{
		ID:        uuid.New().String(),
		UserID:    userID,
		StackID:   stackIDStr,
		Action:    action,
		Detail:    string(detailJSON),
		RequestID: requestID,
		CreatedAt: time.Now(),
	}

	err = l.db.LogAction(actionLog)
	if err != nil {
		slog.Error("failed to log action", "action", action, "error", err)
	}
}
