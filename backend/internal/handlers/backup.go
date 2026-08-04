package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// validSnapshotIDRegex matches restic snapshot identifiers: a short or full hex
// ID, or the literal "latest". Used to reject malformed/flag-like values before
// they reach `restic ls` (M5).
var validSnapshotIDRegex = regexp.MustCompile(`^([0-9a-fA-F]{8,64}|latest)$`)

// BackupHandler serves all /api/settings/backup and /api/backups/* REST
// endpoints. WebSocket streaming routes (/ws/backups/*) are wired separately
// via RegisterWSRoutes (called from main.go).
//
// Durable execution model (Finding #7 / #17):
//
//   - POST /backups/run and POST /backups/restore persist a BackupRun DB row
//     with status="running" and immediately start the operation on a
//     context.Background()-derived goroutine via BackupRunnerRegistry.
//   - The 202 response returns {runId, wsUrl} so existing clients continue to
//     work, but the run PROCEEDS whether or not any WS client ever connects.
//   - The WS endpoints are attach/stream channels: they replay buffered log
//     lines to the client and forward new lines as they arrive.  A WS client
//     disconnect does NOT cancel the run.
//   - GET /backups/runs/:runId returns the durable DB record so clients can
//     poll or reconcile after a disconnect (Finding #17).
type BackupHandler struct {
	svc      *services.BackupService
	db       *database.DB
	logger   *slog.Logger
	registry *services.BackupRunnerRegistry
}

// NewBackupHandler creates a BackupHandler. Call RegisterRoutes and
// RegisterWSRoutes from main.go after construction.
func NewBackupHandler(
	svc *services.BackupService,
	db *database.DB,
	logger *slog.Logger,
) *BackupHandler {
	return &BackupHandler{
		svc:      svc,
		db:       db,
		logger:   logger,
		registry: services.NewBackupRunnerRegistry(db, svc, logger),
	}
}

// Stop stops the handler's durable-run registry: its GC loop and, critically,
// blocks until every in-flight exec goroutine (backup/restore/sync/dr-restore/
// prune) has fully finished, including its terminal DB write. Callers that own
// a BackupHandler's lifecycle — main.go on graceful shutdown, tests via
// t.Cleanup — must call this before releasing anything those goroutines still
// write to (the DB handle, a t.TempDir()), since they run detached on
// context.Background() and nothing else joins them. See agent-os-80n.
func (h *BackupHandler) Stop() {
	h.registry.Stop()
}

// RegisterRoutes registers all backup REST routes under the authenticated
// protected group.
func (h *BackupHandler) RegisterRoutes(group *gin.RouterGroup) {
	// Settings
	group.GET("/settings/backup", h.getSettings)
	group.PUT("/settings/backup", h.updateSettings)

	// Policies
	group.GET("/backups/policies", h.listPolicies)
	group.PUT("/backups/policies/stack/:stackId", h.upsertPolicy)
	group.DELETE("/backups/policies/stack/:stackId", h.deletePolicy)

	// Status & history
	group.GET("/backups/status", h.getStatus)
	group.GET("/backups/history", h.getHistory)

	// Run detail — the durable source of truth for Finding #17.
	// Returns the BackupRun record + run items for the given runId.
	group.GET("/backups/runs/:runId", h.getRunDetail)

	// Snapshots
	group.GET("/backups/snapshots", h.listSnapshots)
	group.GET("/backups/snapshots/:snapshotId/preview", h.previewSnapshot)

	// Operations — kick off durable runs; WS streaming wired in RegisterWSRoutes.
	group.POST("/backups/run", h.runBackup)
	group.POST("/backups/sync", h.runSync)
	group.POST("/backups/restore", h.runRestore)
	group.POST("/backups/dr-restore", h.runDRRestore)
	group.POST("/backups/prune", h.runPrune)

	// Repo / cloud utility
	group.POST("/backups/repo/init", h.repoInit)
	group.POST("/backups/cloud/test", h.cloudTest)
}

// ───────────────────────────────────────────
// Settings
// ───────────────────────────────────────────

func (h *BackupHandler) getSettings(c *gin.Context) {
	db := h.db
	cfg := h.svc.Config()

	bc := services.ResolveBackupConfigWithCfg(db, cfg)
	repoSrc, pwSrc, hasPassword := services.RepoSettingSources(db, cfg)

	repository := bc.ResticRepository

	keepDaily, _ := db.GetSetting("backup_keep_daily")
	keepWeekly, _ := db.GetSetting("backup_keep_weekly")
	keepMonthly, _ := db.GetSetting("backup_keep_monthly")
	keepYearly, _ := db.GetSetting("backup_keep_yearly")
	autoPrune, _ := db.GetSetting("backup_auto_prune")
	scheduleInterval, _ := db.GetSetting("backup_schedule_interval")
	syncAfterBackup, _ := db.GetSetting("backup_sync_after")
	rcloneRemote, _ := db.GetSetting("rclone_remote")
	rclonePath, _ := db.GetSetting("rclone_path")
	rcloneTransfers, _ := db.GetSetting("rclone_transfers")
	hostname, _ := db.GetSetting("backup_hostname")

	av := h.svc.Available()
	repoStatus := h.svc.CheckRepository(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"repository":              repository,
		"repositorySource":        repoSrc,
		"hasPassword":             hasPassword,
		"passwordSource":          pwSrc,
		"keepDaily":               settingIntOrDefault(keepDaily, 7),
		"keepWeekly":              settingIntOrDefault(keepWeekly, 4),
		"keepMonthly":             settingIntOrDefault(keepMonthly, 6),
		"keepYearly":              settingIntOrDefault(keepYearly, 0),
		"autoPrune":               settingBoolOrDefault(autoPrune, true),
		"scheduleIntervalMinutes": settingIntOrDefault(scheduleInterval, 0),
		"syncAfterBackup":         settingBoolOrDefault(syncAfterBackup, false),
		"rcloneRemote":            rcloneRemote,
		"rclonePath":              rclonePath,
		"rcloneTransfers":         settingIntOrDefault(rcloneTransfers, 4),
		"hostname":                hostname,
		"resticAvailable":         av.ResticPresent,
		"rcloneAvailable":         av.RclonePresent,
		"repositoryInitialized":   repoStatus.RepoReachable,
	})
}

// backupSettingsRequest defines the writable fields for PUT /api/settings/backup.
// All pointer fields are optional; nil means "leave unchanged".
// An empty string value clears the DB override (reverts to env fallback).
type backupSettingsRequest struct {
	Repository              *string `json:"repository"`
	Password                *string `json:"password"`
	KeepDaily               *int    `json:"keepDaily"`
	KeepWeekly              *int    `json:"keepWeekly"`
	KeepMonthly             *int    `json:"keepMonthly"`
	KeepYearly              *int    `json:"keepYearly"`
	AutoPrune               *bool   `json:"autoPrune"`
	ScheduleIntervalMinutes *int    `json:"scheduleIntervalMinutes"`
	SyncAfterBackup         *bool   `json:"syncAfterBackup"`
	RcloneRemote            *string `json:"rcloneRemote"`
	RclonePath              *string `json:"rclonePath"`
	RcloneTransfers         *int    `json:"rcloneTransfers"`
	Hostname                *string `json:"hostname"`
}

func (h *BackupHandler) updateSettings(c *gin.Context) {
	var req backupSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	db := h.db
	intervalChanged := false
	var newInterval int

	if req.Repository != nil {
		if err := db.SetSetting("restic_repository", *req.Repository); err != nil {
			h.internalError(c, "Failed to save repository setting", err)
			return
		}
	}
	if req.Password != nil && *req.Password != "" {
		if err := db.SetSetting("restic_password", *req.Password); err != nil {
			if respondIfEncryptionUnavailable(c, err) {
				return
			}
			h.internalError(c, "Failed to save password setting", err)
			return
		}
	}
	if req.KeepDaily != nil {
		if err := db.SetSetting("backup_keep_daily", strconv.Itoa(*req.KeepDaily)); err != nil {
			h.internalError(c, "Failed to save keep_daily setting", err)
			return
		}
	}
	if req.KeepWeekly != nil {
		if err := db.SetSetting("backup_keep_weekly", strconv.Itoa(*req.KeepWeekly)); err != nil {
			h.internalError(c, "Failed to save keep_weekly setting", err)
			return
		}
	}
	if req.KeepMonthly != nil {
		if err := db.SetSetting("backup_keep_monthly", strconv.Itoa(*req.KeepMonthly)); err != nil {
			h.internalError(c, "Failed to save keep_monthly setting", err)
			return
		}
	}
	if req.KeepYearly != nil {
		if err := db.SetSetting("backup_keep_yearly", strconv.Itoa(*req.KeepYearly)); err != nil {
			h.internalError(c, "Failed to save keep_yearly setting", err)
			return
		}
	}
	if req.AutoPrune != nil {
		val := "false"
		if *req.AutoPrune {
			val = "true"
		}
		if err := db.SetSetting("backup_auto_prune", val); err != nil {
			h.internalError(c, "Failed to save auto_prune setting", err)
			return
		}
	}
	if req.ScheduleIntervalMinutes != nil {
		intervalChanged = true
		newInterval = *req.ScheduleIntervalMinutes
		if err := db.SetSetting("backup_schedule_interval", strconv.Itoa(newInterval)); err != nil {
			h.internalError(c, "Failed to save schedule_interval setting", err)
			return
		}
	}
	if req.SyncAfterBackup != nil {
		val := "false"
		if *req.SyncAfterBackup {
			val = "true"
		}
		if err := db.SetSetting("backup_sync_after", val); err != nil {
			h.internalError(c, "Failed to save sync_after setting", err)
			return
		}
	}
	if req.RcloneRemote != nil {
		if err := db.SetSetting("rclone_remote", *req.RcloneRemote); err != nil {
			h.internalError(c, "Failed to save rclone_remote setting", err)
			return
		}
	}
	if req.RclonePath != nil {
		if err := db.SetSetting("rclone_path", *req.RclonePath); err != nil {
			h.internalError(c, "Failed to save rclone_path setting", err)
			return
		}
	}
	if req.RcloneTransfers != nil {
		if err := db.SetSetting("rclone_transfers", strconv.Itoa(*req.RcloneTransfers)); err != nil {
			h.internalError(c, "Failed to save rclone_transfers setting", err)
			return
		}
	}
	if req.Hostname != nil {
		if err := db.SetSetting("backup_hostname", *req.Hostname); err != nil {
			h.internalError(c, "Failed to save hostname setting", err)
			return
		}
	}

	if intervalChanged {
		h.svc.StopScheduler()
		if newInterval > 0 {
			h.svc.StartScheduler()
		}
	}

	h.getSettings(c)
}

// ───────────────────────────────────────────
// Policies
// ───────────────────────────────────────────

func (h *BackupHandler) listPolicies(c *gin.Context) {
	policies, err := h.db.GetBackupPolicies()
	if err != nil {
		h.internalError(c, "Failed to list backup policies", err)
		return
	}
	if policies == nil {
		policies = []models.BackupPolicy{}
	}
	c.JSON(http.StatusOK, gin.H{"policies": policies})
}

type upsertPolicyRequest struct {
	Enabled    bool   `json:"enabled"`
	StopPolicy string `json:"stopPolicy"`
}

func (h *BackupHandler) upsertPolicy(c *gin.Context) {
	stackID := c.Param("stackId")

	if _, err := h.db.GetStack(stackID); err != nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	var req upsertPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	stopPolicy := req.StopPolicy
	if stopPolicy == "" {
		stopPolicy = "stop"
	}
	// Validate against the DB CHECK constraint (stop_policy IN ('stop','hot')).
	if stopPolicy != "stop" && stopPolicy != "hot" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"stopPolicy must be 'stop' or 'hot'",
		))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	existing, _ := h.db.GetBackupPolicy(stackID)

	policy := &models.BackupPolicy{
		ID:         generateID(),
		TargetType: "stack",
		TargetID:   stackID,
		Enabled:    req.Enabled,
		StopPolicy: stopPolicy,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if existing != nil {
		policy.ID = existing.ID
		policy.CreatedAt = existing.CreatedAt
	}

	if err := h.db.UpsertBackupPolicy(policy); err != nil {
		h.internalError(c, "Failed to save backup policy", err)
		return
	}

	BroadcastEvent(models.StackEvent{Type: "backup_policy_changed", Timestamp: time.Now()})
	c.JSON(http.StatusOK, policy)
}

func (h *BackupHandler) deletePolicy(c *gin.Context) {
	stackID := c.Param("stackId")

	if err := h.db.DeleteBackupPolicy(stackID); err != nil {
		h.internalError(c, "Failed to delete backup policy", err)
		return
	}

	c.Status(http.StatusNoContent)
}

// ───────────────────────────────────────────
// Status & history
// ───────────────────────────────────────────

func (h *BackupHandler) getStatus(c *gin.Context) {
	av := h.svc.Available()
	repoStatus := h.svc.CheckRepository(c.Request.Context())

	policies, err := h.db.GetEnabledBackupPolicies()
	if err != nil {
		h.internalError(c, "Failed to get enabled policies", err)
		return
	}

	runs, _ := h.db.GetBackupRuns(1)
	var lastRun *models.BackupRun
	if len(runs) > 0 {
		lastRun = &runs[0]
	}

	var repoSizeBytes *int64
	if repoStatus.RepoReachable {
		repoSizeBytes = h.svc.RepoSizeBytes(c.Request.Context())
	}

	c.JSON(http.StatusOK, gin.H{
		"resticAvailable":       av.ResticPresent,
		"rcloneAvailable":       av.RclonePresent,
		"repositoryInitialized": repoStatus.RepoReachable,
		"enabledStackCount":     len(policies),
		"lastRun":               lastRun,
		"nextRunAt":             h.svc.NextRunAt(),
		"repoSizeBytes":         repoSizeBytes,
		"schedulerRunning":      h.svc.SchedulerRunning(),
	})
}

func (h *BackupHandler) getHistory(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}

	runs, err := h.db.GetBackupRuns(limit)
	if err != nil {
		h.internalError(c, "Failed to get backup history", err)
		return
	}
	if runs == nil {
		runs = []models.BackupRun{}
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

// getRunDetail returns the durable record for a specific run ID, including
// per-stack items. This is the Finding #17 status endpoint: a client that
// disconnected from the WS stream can call this to reconcile to server truth.
//
// Response shape:
//
//	{"run": BackupRun, "items": []BackupRunItem}
func (h *BackupHandler) getRunDetail(c *gin.Context) {
	runID := c.Param("runId")

	run, err := h.db.GetBackupRunByID(runID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"Backup run not found",
		))
		return
	}

	items, err := h.db.GetBackupRunItems(runID)
	if err != nil {
		h.internalError(c, "Failed to fetch backup run items", err)
		return
	}
	if items == nil {
		items = []models.BackupRunItem{}
	}

	c.JSON(http.StatusOK, gin.H{
		"run":   run,
		"items": items,
	})
}

// ───────────────────────────────────────────
// Snapshots
// ───────────────────────────────────────────

func (h *BackupHandler) listSnapshots(c *gin.Context) {
	av := h.svc.Available()
	if !av.ResticPresent {
		c.JSON(http.StatusOK, []models.BackupSnapshot{})
		return
	}

	repoStatus := h.svc.CheckRepository(c.Request.Context())
	if !repoStatus.RepoReachable {
		c.JSON(http.StatusOK, []models.BackupSnapshot{})
		return
	}

	stackID := c.Query("stackId")

	snapshots, err := h.listSnapshotsViaRestic(c.Request.Context(), stackID)
	if err != nil {
		h.internalError(c, "Failed to list snapshots", err)
		return
	}
	if snapshots == nil {
		snapshots = []models.BackupSnapshot{}
	}
	c.JSON(http.StatusOK, snapshots)
}

func (h *BackupHandler) previewSnapshot(c *gin.Context) {
	snapshotID := c.Param("snapshotId")

	if !validSnapshotIDRegex.MatchString(snapshotID) {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid snapshot ID",
		))
		return
	}

	av := h.svc.Available()
	if !av.ResticPresent {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"BACKUP_UNAVAILABLE",
			"restic is not available",
		))
		return
	}

	repoStatus := h.svc.CheckRepository(c.Request.Context())
	if !repoStatus.RepoReachable {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"Repository not initialized or unreachable",
		))
		return
	}

	entries, err := h.previewSnapshotViaRestic(c.Request.Context(), snapshotID)
	if err != nil {
		h.internalError(c, "Failed to preview snapshot", err)
		return
	}
	if entries == nil {
		entries = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// ───────────────────────────────────────────
// Operations — durable kickoff handlers
// ───────────────────────────────────────────

// runBackupRequest is the POST /backups/run request body.
type runBackupRequest struct {
	StackIDs []string `json:"stackIds"`
	DryRun   bool     `json:"dryRun"`
}

// runBackup handles POST /backups/run.
//
// The operation is started immediately on a detached goroutine via
// BackupRunnerRegistry.LaunchBackup. A BackupRun DB record is created with
// status="running" before returning 202, so the run is durable even if no WS
// client ever connects.
func (h *BackupHandler) runBackup(c *gin.Context) {
	var req runBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if err := h.requireAvailable(c); err != nil {
		return
	}

	runID, err := h.registry.LaunchBackup(req.StackIDs, req.DryRun)
	if err != nil {
		h.internalError(c, "Failed to start backup run", err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/run/" + runID,
	})
}

func (h *BackupHandler) runSync(c *gin.Context) {
	if err := h.requireAvailable(c); err != nil {
		return
	}

	runID, err := h.registry.LaunchSync()
	if err != nil {
		h.internalError(c, "Failed to start sync", err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/sync/" + runID,
	})
}

// runRestoreRequest is the POST /backups/restore request body.
type runRestoreRequest struct {
	StackID    string `json:"stackId"`
	SnapshotID string `json:"snapshotId"`
	Target     string `json:"target"`
	Confirm    bool   `json:"confirm"`
}

// runRestore handles POST /backups/restore.
//
// The restore is destructive. A BackupRun DB record is persisted before
// the 202 response is sent, and execution runs on context.Background() so a
// WS client disconnect cannot abort it mid-flight.
func (h *BackupHandler) runRestore(c *gin.Context) {
	var req runRestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if req.StackID == "" || req.SnapshotID == "" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"stackId and snapshotId are required",
		))
		return
	}

	// Restore is destructive: require explicit confirmation.
	if !req.Confirm {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"CONFIRMATION_REQUIRED",
			"Destructive operation: set confirm=true to proceed",
		))
		return
	}

	if _, err := h.db.GetStack(req.StackID); err != nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	if err := h.requireAvailable(c); err != nil {
		return
	}

	runID, err := h.registry.LaunchRestore(req.StackID, req.SnapshotID, req.Target)
	if err != nil {
		h.internalError(c, "Failed to start restore", err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/restore/" + runID,
	})
}

// runDRRestoreRequest is the POST /backups/dr-restore request body.
//
// The restore destination is intentionally NOT accepted from the client: it is
// derived server-side under DataDir by the backup service. A client-supplied
// path previously flowed into `rclone sync`, allowing arbitrary host-path
// overwrite (finding C1). Any localRepoPath field a client sends is ignored.
type runDRRestoreRequest struct {
	Confirm bool `json:"confirm"`
}

func (h *BackupHandler) runDRRestore(c *gin.Context) {
	var req runDRRestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if !req.Confirm {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"CONFIRMATION_REQUIRED",
			"Destructive operation: set confirm=true to proceed",
		))
		return
	}

	av := h.svc.Available()
	if !av.RclonePresent {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"BACKUP_UNAVAILABLE",
			"rclone is not available",
		))
		return
	}

	if h.svc.IsBusy() {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"BACKUP_BUSY",
			"A backup operation is already in progress",
		))
		return
	}

	runID, err := h.registry.LaunchDRRestore()
	if err != nil {
		h.internalError(c, "Failed to start DR restore", err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/dr-restore/" + runID,
	})
}

// runPruneRequest is the POST /backups/prune request body.
type runPruneRequest struct {
	Confirm bool `json:"confirm"`
	DryRun  bool `json:"dryRun"`
}

func (h *BackupHandler) runPrune(c *gin.Context) {
	var req runPruneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if !req.Confirm && !req.DryRun {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			"CONFIRMATION_REQUIRED",
			"Destructive operation: set confirm=true (or dryRun=true) to proceed",
		))
		return
	}

	if err := h.requireAvailable(c); err != nil {
		return
	}

	runID, err := h.registry.LaunchPrune(req.DryRun)
	if err != nil {
		h.internalError(c, "Failed to start prune", err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/prune/" + runID,
	})
}

// ───────────────────────────────────────────
// Repo init & cloud test
// ───────────────────────────────────────────

func (h *BackupHandler) repoInit(c *gin.Context) {
	av := h.svc.Available()
	if !av.ResticPresent {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"BACKUP_UNAVAILABLE",
			"restic is not available",
		))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	repoStatus := h.svc.CheckRepository(ctx)
	if repoStatus.RepoReachable {
		c.JSON(http.StatusOK, gin.H{"initialized": true})
		return
	}

	restic := h.svc.NewResticManager()

	if err := restic.EnsureRepository(ctx); err != nil {
		h.internalError(c, "Failed to initialise repository", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"initialized": true})
}

func (h *BackupHandler) cloudTest(c *gin.Context) {
	av := h.svc.Available()
	if !av.RclonePresent {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"BACKUP_UNAVAILABLE",
			"rclone is not available",
		))
		return
	}

	bc := h.svc.ResolveConfig()

	if bc.RcloneRemote == "" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"rclone remote is not configured",
		))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	rclone := h.svc.NewRcloneManager()
	if err := rclone.TestConnectivity(ctx, bc.RcloneRemote); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ───────────────────────────────────────────
// WebSocket streaming routes
// ───────────────────────────────────────────

// RegisterWSRoutes registers the five backup streaming endpoints on the shared
// wsGroup. Each endpoint upgrades to a WebSocket and attaches to an already-
// running (or finished) durable run via BackupRunnerRegistry.Attach.
//
// Terminal WS frame shape:
//
//	{"type":"done","outcome":"success"|"partial"|"failed","reason":"..."}
//
// A client disconnect DOES NOT cancel the underlying operation.
func (h *BackupHandler) RegisterWSRoutes(group *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	group.GET("/ws/backups/run/:runId", h.wsAttach(jwtSecret, authDisabled, "backup"))
	group.GET("/ws/backups/sync/:runId", h.wsAttach(jwtSecret, authDisabled, "sync"))
	group.GET("/ws/backups/restore/:runId", h.wsAttach(jwtSecret, authDisabled, "restore"))
	group.GET("/ws/backups/dr-restore/:runId", h.wsAttach(jwtSecret, authDisabled, "dr-restore"))
	group.GET("/ws/backups/prune/:runId", h.wsAttach(jwtSecret, authDisabled, "prune"))
}

// wsAttach returns a handler that attaches a WS client to a durable run.
//
// Protocol:
//  1. Upgrade the HTTP connection to WS.
//  2. Look up the run in the registry via Attach.
//  3. Send a {"type":"start","action":"<action>"} frame.
//  4. Replay all buffered log lines as {"type":"data","line":"..."} frames.
//  5. If the run is already done, send the terminal frame and close.
//  6. Otherwise, stream live lines until the run finishes or the client
//     disconnects. The client disconnect closes the WS write loop but does NOT
//     cancel the underlying goroutine.
//  7. When the run finishes, send the terminal done frame.
func (h *BackupHandler) wsAttach(jwtSecret string, authDisabled bool, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		runID := c.Param("runId")

		// Validate the run exists BEFORE upgrading so a 404 can still be sent as
		// plain HTTP (once upgraded, only WS close frames are possible).
		// Use nil clientGone for the pre-flight check — forwardLive is not started
		// at this stage, so there is no goroutine to leak.
		if _, err := h.registry.Attach(runID, nil); err != nil {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"Backup run not found: "+err.Error(),
			))
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		// wsCtx is cancelled on client disconnect; it governs only the write loop.
		// The underlying operation runs on context.Background() and is not affected.
		wsCtx, wsCancel := context.WithCancel(context.Background())
		defer wsCancel()

		// Read pump: cancels wsCtx on disconnect or error; does not cancel the op.
		go func() {
			for {
				if _, _, err := conn.Conn.ReadMessage(); err != nil {
					wsCancel()
					return
				}
			}
		}()

		go safePingLoop(wsCtx, conn, DefaultPingInterval)

		// Best-effort notification; a write failure here surfaces on the
		// next read/ping and the connection is torn down there.
		_ = safeWriteJSON(conn, gin.H{"type": "start", "action": action})

		// Real attach: pass wsCtx.Done() so forwardLive exits promptly on
		// client disconnect instead of blocking on a full buffer (Fix #2).
		attached, err := h.registry.Attach(runID, wsCtx.Done())
		if err != nil {
			// Extremely unlikely (run evicted between pre-flight and here), but
			// handle gracefully by sending a terminal failed frame.
			h.sendDoneFrame(conn, runID, "failed", "run evicted between check and attach")
			return
		}

		// Replay buffered lines.
		for _, line := range attached.ReplayLines {
			if wsCtx.Err() != nil {
				return
			}
			if err := safeWriteJSON(conn, gin.H{"type": "data", "line": line.Line}); err != nil {
				h.logger.Debug("WS write error during replay", "run_id", runID, "error", err)
				return
			}
		}

		if attached.Done {
			h.sendDoneFrame(conn, runID, attached.Outcome, attached.Reason)
			return
		}

		// Stream live lines until done or disconnect.
		for {
			select {
			case <-wsCtx.Done():
				// Client disconnected — stop writing but the op continues.
				// forwardLive will detect clientGone and exit on its own.
				h.logger.Info("WS client disconnected; op continues",
					"run_id", runID, "action", action)
				return

			case line, ok := <-attached.Live:
				if !ok {
					// Live channel closed — run finished; fetch terminal outcome.
					outcome, reason := h.outcomeFromRegistry(runID)
					h.sendDoneFrame(conn, runID, outcome, reason)
					return
				}
				if err := safeWriteJSON(conn, gin.H{"type": "data", "line": line.Line}); err != nil {
					h.logger.Debug("WS write error during stream", "run_id", runID, "error", err)
					return
				}
			}
		}
	}
}

// outcomeFromRegistry retrieves the terminal outcome for runID from the
// registry (in-memory) or the DB (if evicted).
func (h *BackupHandler) outcomeFromRegistry(runID string) (outcome, reason string) {
	// Read-only terminal-outcome lookup for an already-finished run; no live
	// stream is started, so clientGone is nil (matches the kickoff-path usage).
	attached, err := h.registry.Attach(runID, nil)
	if err != nil {
		return "failed", "could not read run outcome: " + err.Error()
	}
	return attached.Outcome, attached.Reason
}

// sendDoneFrame writes the terminal WS frame and logs completion.
// Shape: {"type":"done","outcome":"success"|"partial"|"failed","reason":"..."}
func (h *BackupHandler) sendDoneFrame(conn *Connection, runID, outcome, reason string) {
	if err := safeWriteJSON(conn, gin.H{
		"type":    "done",
		"outcome": outcome,
		"reason":  reason,
	}); err != nil {
		h.logger.Debug("Failed to write done frame", "run_id", runID, "error", err)
	}
	h.logger.Info("Backup WS operation completed",
		"run_id", runID,
		"outcome", outcome,
	)
}

// ───────────────────────────────────────────
// Private helpers
// ───────────────────────────────────────────

// requireAvailable checks engine availability and the busy flag.
// On failure it writes a 409 response and returns a non-nil error.
func (h *BackupHandler) requireAvailable(c *gin.Context) error {
	av := h.svc.Available()
	if !av.Available {
		err := models.NewAppError(
			http.StatusConflict,
			"BACKUP_UNAVAILABLE",
			av.Message,
		)
		c.JSON(http.StatusConflict, err)
		return err
	}

	if h.svc.IsBusy() {
		err := models.NewAppError(
			http.StatusConflict,
			"BACKUP_BUSY",
			"A backup operation is already in progress",
		)
		c.JSON(http.StatusConflict, err)
		return err
	}
	return nil
}

func (h *BackupHandler) internalError(c *gin.Context, msg string, err error) {
	if err != nil {
		h.logger.Error(msg, "error", err)
	}
	c.JSON(http.StatusInternalServerError, models.NewAppError(
		http.StatusInternalServerError,
		"INTERNAL_ERROR",
		msg,
	))
}

// listSnapshotsViaRestic builds a ResticManager and returns all snapshots for
// the given tag (empty = all).
func (h *BackupHandler) listSnapshotsViaRestic(ctx context.Context, stackID string) ([]models.BackupSnapshot, error) {
	restic := h.svc.NewResticManager()
	return restic.ListSnapshots(ctx, stackID, 0)
}

// previewSnapshotViaRestic runs restic ls and collects the output lines.
func (h *BackupHandler) previewSnapshotViaRestic(ctx context.Context, snapshotID string) ([]string, error) {
	restic := h.svc.NewResticManager()

	out := make(chan services.StreamLine, 256)
	done := make(chan error, 1)

	go func() {
		done <- restic.RestorePreview(ctx, snapshotID, out)
		close(out)
	}()

	var entries []string
	for line := range out {
		if line.Line != "" {
			entries = append(entries, line.Line)
		}
	}

	if err := <-done; err != nil {
		return nil, err
	}
	return entries, nil
}

// settingIntOrDefault parses a DB setting string, returning defaultVal on
// empty or parse error.
func settingIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// settingBoolOrDefault returns the bool value of a DB setting string, falling
// back to defaultVal when the string is empty.
func settingBoolOrDefault(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	return s == "true"
}

// generateID returns a new UUID string.
func generateID() string {
	return uuid.New().String()
}
