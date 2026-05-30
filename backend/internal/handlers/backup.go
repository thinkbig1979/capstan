package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// ───────────────────────────────────────────
// In-memory pending-operation registry
// ───────────────────────────────────────────

// pendingOpKind names the five streamable operation types.
type pendingOpKind string

const (
	opKindRun       pendingOpKind = "run"
	opKindSync      pendingOpKind = "sync"
	opKindRestore   pendingOpKind = "restore"
	opKindDRRestore pendingOpKind = "dr-restore"
	opKindPrune     pendingOpKind = "prune"
)

// pendingOp holds the parameters stashed by the POST handler so the WS handler
// can run the operation without repeating validation or re-parsing query strings.
//
// Design note — why in-memory registry (approach B) over stateless query params:
//   - POST already validates stack existence, availability, confirm gates, and
//     parses the JSON body. The registry lets the WS handler stay thin and
//     avoids re-doing that work over query strings.
//   - stackIds arrays and complex fields are awkward to URL-encode.
//   - The runId in wsUrl is a correlation/session id only; the real backup_runs
//     rows are created and finalized by BackupService, which generates its own
//     UUIDs. GET /backups/history/runs surfaces those rows to the UI.
//   - Entries expire after 5 minutes to guard against abandoned connections.
type pendingOp struct {
	kind      pendingOpKind
	expiresAt time.Time

	// run
	stackIDs []string
	dryRun   bool

	// restore
	stackID    string
	snapshotID string
	target     string

	// dr-restore
	localRepoPath string
}

// pendingOps is the package-level registry of operations waiting for a WS
// connection. A short 5-minute TTL prevents unbounded growth when the client
// never connects after the POST.
var (
	pendingOpsMu sync.Mutex
	pendingOps   = make(map[string]*pendingOp)
)

const pendingOpTTL = 5 * time.Minute

// stashOp stores op under runID and returns it.
func stashOp(runID string, op *pendingOp) {
	op.expiresAt = time.Now().Add(pendingOpTTL)
	pendingOpsMu.Lock()
	defer pendingOpsMu.Unlock()
	// Evict expired entries on every write to bound memory.
	for id, p := range pendingOps {
		if time.Now().After(p.expiresAt) {
			delete(pendingOps, id)
		}
	}
	pendingOps[runID] = op
}

// popOp atomically retrieves and deletes the pending op for runID.
// Returns nil when the runID is unknown or expired.
func popOp(runID string) *pendingOp {
	pendingOpsMu.Lock()
	defer pendingOpsMu.Unlock()
	op, ok := pendingOps[runID]
	if !ok {
		return nil
	}
	delete(pendingOps, runID)
	if time.Now().After(op.expiresAt) {
		return nil
	}
	return op
}

// BackupHandler serves all /api/settings/backup and /api/backups/* REST
// endpoints. WebSocket streaming routes (/ws/backups/*) are wired in a
// separate task (a70.9) via the wsGroup; this file registers only the
// protected REST group.
type BackupHandler struct {
	svc    *services.BackupService
	db     *database.DB
	logger *slog.Logger
}

// NewBackupHandler creates a BackupHandler. a70.9 will call RegisterRoutes
// from main.go after construction.
func NewBackupHandler(svc *services.BackupService, db *database.DB, logger *slog.Logger) *BackupHandler {
	return &BackupHandler{
		svc:    svc,
		db:     db,
		logger: logger,
	}
}

// RegisterRoutes registers all backup REST routes under the authenticated
// protected group, matching the API contract in api-spec.md.
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

	// Runs
	group.GET("/backups/runs/:runId", h.getRunDetail)

	// Snapshots
	group.GET("/backups/snapshots", h.listSnapshots)
	group.GET("/backups/snapshots/:snapshotId/preview", h.previewSnapshot)

	// Operations (kick off; WS streaming wired in a70.9)
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

	repository, _ := db.GetSetting("restic_repository")
	repoSource := "default"
	if repository != "" {
		repoSource = "db"
	}

	// Password must NEVER be returned in the response.
	// Report only a boolean indicating whether one is configured.
	pwDB, _ := db.GetSetting("restic_password")
	hasPassword := pwDB != ""
	passwordSource := "default"
	if pwDB != "" {
		passwordSource = "db"
	}

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
		"repositorySource":        repoSource,
		"hasPassword":             hasPassword,
		"passwordSource":          passwordSource,
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

	// For each field: if pointer is non-nil write the value (empty string clears
	// the DB override back to env fallback). Password: empty string = no-op
	// (spec: a separate explicit "clear" is required to remove the password).
	if req.Repository != nil {
		if err := db.SetSetting("restic_repository", *req.Repository); err != nil {
			h.internalError(c, "Failed to save repository setting", err)
			return
		}
	}
	if req.Password != nil && *req.Password != "" {
		// SetSetting encrypts sensitive keys (restic_password) at rest.
		if err := db.SetSetting("restic_password", *req.Password); err != nil {
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

	// Restart scheduler if interval changed (mirrors UpdateUpdateSettings pattern).
	if intervalChanged {
		h.svc.StopScheduler()
		if newInterval > 0 {
			h.svc.StartScheduler()
		}
	}

	// Return the updated effective settings.
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

	// Validate the stack exists before creating a policy for it.
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

	now := time.Now().UTC().Format(time.RFC3339)

	existing, _ := h.db.GetBackupPolicy(stackID)

	policy := &models.BackupPolicy{
		ID:         uuid.New().String(),
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

	// Broadcast so the UI can update, mirroring update_policy_changed.
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

	c.JSON(http.StatusOK, gin.H{
		"resticAvailable":       av.ResticPresent,
		"rcloneAvailable":       av.RclonePresent,
		"repositoryInitialized": repoStatus.RepoReachable,
		"enabledStackCount":     len(policies),
		"lastRun":               lastRun,
		"nextRunAt":             nil,
		"repoSizeBytes":         nil,
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

func (h *BackupHandler) getRunDetail(c *gin.Context) {
	runID := c.Param("runId")

	// The DB has no GetBackupRunByID; fetch with a generous limit and find.
	runs, err := h.db.GetBackupRuns(1000)
	if err != nil {
		h.internalError(c, "Failed to fetch backup runs", err)
		return
	}

	var found *models.BackupRun
	for i := range runs {
		if runs[i].ID == runID {
			found = &runs[i]
			break
		}
	}
	if found == nil {
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
		"run":   found,
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
// Operations
// ───────────────────────────────────────────

type runBackupRequest struct {
	StackIDs []string `json:"stackIds"`
	DryRun   bool     `json:"dryRun"`
}

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

	// Stash params for the WS handler; the operation runs when the client
	// connects to the websocket, mirroring the OperationsHandler pattern.
	runID := uuid.New().String()
	stashOp(runID, &pendingOp{
		kind:     opKindRun,
		stackIDs: req.StackIDs,
		dryRun:   req.DryRun,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/run/" + runID,
	})
}

func (h *BackupHandler) runSync(c *gin.Context) {
	if err := h.requireAvailable(c); err != nil {
		return
	}

	runID := uuid.New().String()
	stashOp(runID, &pendingOp{kind: opKindSync})

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/sync/" + runID,
	})
}

type runRestoreRequest struct {
	StackID    string `json:"stackId"`
	SnapshotID string `json:"snapshotId"`
	Target     string `json:"target"`
	Confirm    bool   `json:"confirm"`
}

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

	// Restore is destructive: require explicit confirmation (mirrors
	// dr-restore/prune). The frontend obtains this via a ConfirmDialog; a direct
	// API call must set confirm=true.
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

	runID := uuid.New().String()
	stashOp(runID, &pendingOp{
		kind:       opKindRestore,
		stackID:    req.StackID,
		snapshotID: req.SnapshotID,
		target:     req.Target,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/restore/" + runID,
	})
}

type runDRRestoreRequest struct {
	Confirm       bool   `json:"confirm"`
	LocalRepoPath string `json:"localRepoPath"`
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

	runID := uuid.New().String()
	stashOp(runID, &pendingOp{
		kind:          opKindDRRestore,
		localRepoPath: req.LocalRepoPath,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"runId": runID,
		"wsUrl": "/ws/backups/dr-restore/" + runID,
	})
}

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

	// Prune is destructive: require explicit confirmation unless it is a dry run.
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

	runID := uuid.New().String()
	stashOp(runID, &pendingOp{
		kind:   opKindPrune,
		dryRun: req.DryRun,
	})

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

	bc := services.ResolveBackupConfig(h.db)
	restic := services.NewResticManager(bc, h.logger)

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

	bc := services.ResolveBackupConfig(h.db)

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

	rclone := services.NewRcloneManager(bc, h.logger)
	if err := rclone.TestConnectivity(ctx, bc.RcloneRemote); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
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
	bc := services.ResolveBackupConfig(h.db)
	restic := services.NewResticManager(bc, h.logger)
	return restic.ListSnapshots(ctx, stackID, 0)
}

// previewSnapshotViaRestic runs restic ls and collects the output lines.
func (h *BackupHandler) previewSnapshotViaRestic(ctx context.Context, snapshotID string) ([]string, error) {
	bc := services.ResolveBackupConfig(h.db)
	restic := services.NewResticManager(bc, h.logger)

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

// ───────────────────────────────────────────
// WebSocket streaming routes
// ───────────────────────────────────────────

// RegisterWSRoutes registers the five backup streaming endpoints on the shared
// wsGroup (the same group that OperationsHandler uses). Each route upgrades the
// HTTP connection to a WebSocket, pops the pending op stashed by the POST
// handler, runs the service method, and streams each StreamLine to the client
// exactly like OperationsHandler.handleOperation.
func (h *BackupHandler) RegisterWSRoutes(group *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	group.GET("/ws/backups/run/:runId", h.wsBackupRun(jwtSecret, authDisabled))
	group.GET("/ws/backups/sync/:runId", h.wsBackupSync(jwtSecret, authDisabled))
	group.GET("/ws/backups/restore/:runId", h.wsBackupRestore(jwtSecret, authDisabled))
	group.GET("/ws/backups/dr-restore/:runId", h.wsBackupDRRestore(jwtSecret, authDisabled))
	group.GET("/ws/backups/prune/:runId", h.wsBackupPrune(jwtSecret, authDisabled))
}

// wsBackupRun streams output for a POST /backups/run operation.
func (h *BackupHandler) wsBackupRun(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		runID := c.Param("runId")

		op := popOp(runID)
		if op == nil || op.kind != opKindRun {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"No pending backup run found for this runId (expired or unknown)",
			))
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _, err := conn.Conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()

		safeWriteJSON(conn, gin.H{"type": "start", "action": "run"})

		out := make(chan services.StreamLine, 256)
		opErrCh := make(chan error, 1)

		go func() {
			defer close(out)
			_, err := h.svc.RunBackup(ctx, op.stackIDs, op.dryRun, services.TriggerManual, out)
			opErrCh <- err
		}()

		h.streamAndFinalize(ctx, conn, out, opErrCh, runID, "backup")
	}
}

// wsBackupSync streams output for a POST /backups/sync operation.
func (h *BackupHandler) wsBackupSync(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		runID := c.Param("runId")

		op := popOp(runID)
		if op == nil || op.kind != opKindSync {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"No pending sync operation found for this runId (expired or unknown)",
			))
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _, err := conn.Conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()

		safeWriteJSON(conn, gin.H{"type": "start", "action": "sync"})

		out := make(chan services.StreamLine, 256)
		opErrCh := make(chan error, 1)

		go func() {
			defer close(out)
			opErrCh <- h.svc.RunSync(ctx, out)
		}()

		h.streamAndFinalize(ctx, conn, out, opErrCh, runID, "sync")
	}
}

// wsBackupRestore streams output for a POST /backups/restore operation.
func (h *BackupHandler) wsBackupRestore(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		runID := c.Param("runId")

		op := popOp(runID)
		if op == nil || op.kind != opKindRestore {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"No pending restore operation found for this runId (expired or unknown)",
			))
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _, err := conn.Conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()

		safeWriteJSON(conn, gin.H{"type": "start", "action": "restore"})

		out := make(chan services.StreamLine, 256)
		opErrCh := make(chan error, 1)

		go func() {
			defer close(out)
			opErrCh <- h.svc.RunRestore(ctx, op.stackID, op.snapshotID, op.target, out)
		}()

		h.streamAndFinalize(ctx, conn, out, opErrCh, runID, "restore")
	}
}

// wsBackupDRRestore streams output for a POST /backups/dr-restore operation.
func (h *BackupHandler) wsBackupDRRestore(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		runID := c.Param("runId")

		op := popOp(runID)
		if op == nil || op.kind != opKindDRRestore {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"No pending DR-restore operation found for this runId (expired or unknown)",
			))
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _, err := conn.Conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()

		safeWriteJSON(conn, gin.H{"type": "start", "action": "dr-restore"})

		out := make(chan services.StreamLine, 256)
		opErrCh := make(chan error, 1)

		go func() {
			defer close(out)
			opErrCh <- h.svc.RunDRRestore(ctx, op.localRepoPath, out)
		}()

		h.streamAndFinalize(ctx, conn, out, opErrCh, runID, "dr-restore")
	}
}

// wsBackupPrune streams output for a POST /backups/prune operation.
func (h *BackupHandler) wsBackupPrune(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		runID := c.Param("runId")

		op := popOp(runID)
		if op == nil || op.kind != opKindPrune {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"No pending prune operation found for this runId (expired or unknown)",
			))
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _, err := conn.Conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()

		safeWriteJSON(conn, gin.H{"type": "start", "action": "prune"})

		out := make(chan services.StreamLine, 256)
		opErrCh := make(chan error, 1)

		go func() {
			defer close(out)
			opErrCh <- h.svc.Prune(ctx, op.dryRun, out)
		}()

		h.streamAndFinalize(ctx, conn, out, opErrCh, runID, "prune")
	}
}

// streamAndFinalize ranges over out forwarding each StreamLine to the client as
// a {"type":"data","line":"..."} frame, then sends a final {"type":"done",...}
// frame once the channel is closed. It mirrors the OperationsHandler pattern
// exactly. Respects ctx cancellation (client disconnect) — stops writing early.
//
// opErrCh must be a buffered chan(1) whose single value is sent by the service
// goroutine before it closes out; this function reads from it after the range.
func (h *BackupHandler) streamAndFinalize(
	ctx context.Context,
	conn *Connection,
	out <-chan services.StreamLine,
	opErrCh <-chan error,
	runID string,
	action string,
) {
	for line := range out {
		if ctx.Err() != nil {
			// Client disconnected — drain the channel so the service goroutine
			// can finish writing without blocking.
			go func() {
				for range out {
				}
			}()
			return
		}
		if err := safeWriteJSON(conn, gin.H{
			"type": "data",
			"line": line.Line,
		}); err != nil {
			h.logger.Debug("Failed to write backup stream line", "run_id", runID, "error", err)
			go func() {
				for range out {
				}
			}()
			return
		}
	}

	// Channel is closed: collect the service error and send a done frame.
	opErr := <-opErrCh

	success := opErr == nil
	doneMsg := gin.H{"type": "done", "success": success}
	if opErr != nil {
		doneMsg["error"] = opErr.Error()
	}

	if err := safeWriteJSON(conn, doneMsg); err != nil {
		h.logger.Debug("Failed to write backup done frame", "run_id", runID, "error", err)
	}

	h.logger.Info("Backup WS operation completed",
		"run_id", runID,
		"action", action,
		"success", success,
	)
}
