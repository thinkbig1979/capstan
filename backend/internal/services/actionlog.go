package services

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
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
	ActionLogout      = "logout"
	ActionSetup       = "setup"
	ActionScan        = "scan"
	ActionBackup      = "backup"
	ActionRestore     = "restore"
)

type ActionLogger struct {
	db *database.DB
}

func NewActionLogger(db *database.DB) *ActionLogger {
	return &ActionLogger{db: db}
}

func (l *ActionLogger) Log(userID string, stackID *string, action string, detail interface{}) {
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
		CreatedAt: time.Now(),
	}

	err = l.db.LogAction(actionLog)
	if err != nil {
		slog.Error("failed to log action", "action", action, "error", err)
	}
}

func (l *ActionLogger) LogFromContext(c *gin.Context, stackID *string, action string, detail interface{}) {
	userID := c.GetString("userID")
	if userID == "" {
		userID = "anonymous"
	}
	l.Log(userID, stackID, action, detail)
}
