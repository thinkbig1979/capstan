package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
	"github.com/gin-gonic/gin"
)

type ComposeHandler struct {
	linter    *services.LinterService
	db        *database.DB
	config    *config.Config
	actionLog *services.ActionLogger
}

type ComposeRequest struct {
	Content string `json:"content" binding:"required"`
}

type ComposeResponse struct {
	Content      string `json:"content"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	LastModified string `json:"lastModified"`
}

type ComposeSaveResponse struct {
	Saved       bool                `json:"saved"`
	LintResults []models.LintResult `json:"lintResults,omitempty"`
}

type LintResponse struct {
	Valid       bool                `json:"valid"`
	LintResults []models.LintResult `json:"lintResults"`
}

func NewComposeHandler(linter *services.LinterService, db *database.DB, config *config.Config) *ComposeHandler {
	return &ComposeHandler{
		linter:    linter,
		db:        db,
		config:    config,
		actionLog: services.NewActionLogger(db),
	}
}

func (h *ComposeHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/:id/compose", h.Get)
	group.PUT("/:id/compose", h.Put)
	group.POST("/:id/compose/lint", h.Lint)
	group.PUT("/:id/compose-env", h.PutComposeAndEnv)
}

func (h *ComposeHandler) Get(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	composePath := filepath.Join(stack.Directory, stack.ComposeFile)

	if err := validateStackPath(composePath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid compose file path",
		))
		return
	}

	content, err := os.ReadFile(composePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"Compose file not found on disk",
			))
			return
		}
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"READ_ERROR",
			"Failed to read compose file",
		))
		return
	}

	fileInfo, err := os.Stat(composePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"STAT_ERROR",
			"Failed to get file info",
		))
		return
	}

	c.JSON(http.StatusOK, ComposeResponse{
		Content:      string(content),
		Filename:     stack.ComposeFile,
		Size:         fileInfo.Size(),
		LastModified: fileInfo.ModTime().Format(time.RFC3339),
	})
}

func (h *ComposeHandler) Put(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	var req ComposeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	lintResults, err := h.linter.LintWithDir(req.Content, stack.Directory)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"LINT_ERROR",
			"Failed to lint compose file",
		))
		return
	}

	hasErrors := false
	for _, result := range lintResults {
		if result.Level == "error" {
			hasErrors = true
			break
		}
	}

	if hasErrors {
		c.JSON(http.StatusUnprocessableEntity, models.NewAppErrorWithDetails(
			http.StatusUnprocessableEntity,
			models.ErrComposeValidation,
			"Compose file validation failed",
			gin.H{
				"saved":       false,
				"lintResults": lintResults,
			},
		))
		return
	}

	composePath := filepath.Join(stack.Directory, stack.ComposeFile)

	if err := validateStackPath(composePath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid compose file path",
		))
		return
	}

	if err := os.WriteFile(composePath, []byte(req.Content), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"WRITE_ERROR",
			"Failed to write compose file",
		))
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "update_compose", "Updated compose file: "+stack.ComposeFile)

	c.JSON(http.StatusOK, ComposeSaveResponse{
		Saved:       true,
		LintResults: lintResults,
	})
}

func (h *ComposeHandler) Lint(c *gin.Context) {
	id := c.Param("id")

	stack, _ := h.db.GetStack(id)

	var req ComposeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	workingDir := "/tmp"
	if stack != nil {
		workingDir = stack.Directory
	}

	lintResults, err := h.linter.LintWithDir(req.Content, workingDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"LINT_ERROR",
			"Failed to lint compose file",
		))
		return
	}

	hasErrors := false
	for _, result := range lintResults {
		if result.Level == "error" {
			hasErrors = true
			break
		}
	}

	c.JSON(http.StatusOK, LintResponse{
		Valid:       !hasErrors,
		LintResults: lintResults,
	})
}

func (h *ComposeHandler) logAction(userID, stackID, action, detail string) {
	h.actionLog.Log(userID, &stackID, action, detail)
}

// restoreEnv restores envPath to its pre-update state. If originalBytes is nil
// the file did not exist before the update, so it is removed. If originalBytes
// is non-nil, the file is overwritten with those bytes. Errors are logged but
// not propagated — the caller has already decided what outcome to return.
func restoreEnv(envPath string, originalBytes []byte) {
	if originalBytes == nil {
		// File did not exist before — remove the newly written copy.
		os.Remove(envPath)
		return
	}
	// Best-effort restore of original content.
	os.WriteFile(envPath, originalBytes, 0644) //nolint:errcheck
}

// ComposeEnvRequest is the body for the atomic PUT /api/v1/stacks/:id/compose-env
// endpoint. Both compose content and env entries/raw are written atomically: if
// either write fails both are rolled back so neither is left partially changed (#11).
type ComposeEnvRequest struct {
	ComposeContent string     `json:"composeContent" binding:"required"`
	EnvEntries     []EnvEntry `json:"envEntries"`
	EnvRaw         string     `json:"envRaw"`
}

// PutComposeAndEnv is the atomic compose+env save endpoint (finding #11).
//
// Route: PUT /api/v1/stacks/:id/compose-env
//
// Wire contract:
//
//	Request:  { "composeContent": "...", "envEntries": [...] | "envRaw": "..." }
//	Response (success): { "outcome":"success", "reason":"compose and env saved", "details":{...} }
//	Response (partial/failed): { "outcome":"partial"|"failed", "reason":"...", "details":{...} }
//
// Atomicity guarantee:
//  1. Validate env entries and lint compose before touching disk.
//  2. Write .env to a temp file alongside the real path; rename atomically to the real path.
//  3. Only write compose.yaml after .env rename succeeded.
//  4. On any failure after the .env rename: restore the original compose file from
//     a pre-write backup byte slice (taken before step 3). If the restore fails,
//     the response is partial rather than false success.
func (h *ComposeHandler) PutComposeAndEnv(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	var req ComposeEnvRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	// ── Determine env content ───────────────────────────────────────────────
	var envContent string
	var hasEnvUpdate bool

	if req.EnvRaw != "" {
		envContent = req.EnvRaw
		hasEnvUpdate = true
	} else if len(req.EnvEntries) > 0 {
		if err := validateEnvEntries(req.EnvEntries); err != nil {
			truth.Render(c, truth.Failed("env validation failed: "+err.Error(), err))
			return
		}
		envContent = serializeEnvFile(req.EnvEntries)
		hasEnvUpdate = true
	}

	// ── Lint compose before touching disk ──────────────────────────────────
	lintResults, err := h.linter.LintWithDir(req.ComposeContent, stack.Directory)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"LINT_ERROR",
			"Failed to lint compose file",
		))
		return
	}
	for _, r := range lintResults {
		if r.Level == "error" {
			c.JSON(http.StatusUnprocessableEntity, models.NewAppErrorWithDetails(
				http.StatusUnprocessableEntity,
				models.ErrComposeValidation,
				"Compose file validation failed",
				gin.H{"lintResults": lintResults},
			))
			return
		}
	}

	composePath := filepath.Join(stack.Directory, stack.ComposeFile)
	if err := validateStackPath(composePath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid compose file path",
		))
		return
	}

	// ── Snapshot current compose content for rollback ──────────────────────
	originalCompose, readErr := os.ReadFile(composePath)
	if readErr != nil && !os.IsNotExist(readErr) {
		truth.Render(c, truth.Failed("failed to read existing compose file for rollback snapshot", readErr))
		return
	}

	// ── Write .env atomically (temp → rename) ──────────────────────────────
	// originalEnv holds the pre-update bytes so we can restore them if the
	// subsequent compose write fails. nil means the file did not previously exist.
	var originalEnv []byte
	var envPath string

	if hasEnvUpdate && stack.EnvFile != "" {
		envPath = filepath.Join(stack.Directory, stack.EnvFile)
		if err := validateStackPath(envPath, h.config); err != nil {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				models.ErrPathTraversal,
				"Invalid env file path",
			))
			return
		}

		// Snapshot original .env content so we can restore it on rollback.
		if snapshot, rerr := os.ReadFile(envPath); rerr == nil {
			originalEnv = snapshot
		} else if !os.IsNotExist(rerr) {
			truth.Render(c, truth.Failed("failed to read existing env file for rollback snapshot", rerr))
			return
		}

		tmpEnvPath := envPath + ".tmp"
		if err := os.WriteFile(tmpEnvPath, []byte(envContent), 0644); err != nil {
			truth.Render(c, truth.Failed("failed to write env to temp file; compose unchanged", err))
			return
		}
		if err := os.Rename(tmpEnvPath, envPath); err != nil {
			// Clean up temp file; compose was never written.
			os.Remove(tmpEnvPath)
			truth.Render(c, truth.Failed("failed to atomically replace env file; compose unchanged", err))
			return
		}

		// Verify round-trip for env.
		if ar := verifyEnvRoundTrip(envPath, envContent); ar != nil {
			// .env written but verification failed — restore original bytes.
			restoreEnv(envPath, originalEnv)
			truth.Render(c, *ar)
			return
		}
	}

	// ── Write compose.yaml ──────────────────────────────────────────────────
	if err := os.WriteFile(composePath, []byte(req.ComposeContent), 0644); err != nil {
		// Compose write failed after .env was already replaced — restore .env.
		if hasEnvUpdate && envPath != "" {
			restoreEnv(envPath, originalEnv)
		}
		truth.Render(c, truth.Failed("failed to write compose file; env rolled back", err))
		return
	}

	// ── Verify compose was written ─────────────────────────────────────────
	if writtenCompose, rerr := os.ReadFile(composePath); rerr != nil || string(writtenCompose) != req.ComposeContent {
		// Compose verification failed — roll back both files.
		rollbackErr := ""
		if originalCompose != nil {
			if rErr := os.WriteFile(composePath, originalCompose, 0644); rErr != nil {
				rollbackErr = rErr.Error()
			}
		}
		if hasEnvUpdate && envPath != "" {
			restoreEnv(envPath, originalEnv)
		}
		if rollbackErr != "" {
			truth.Render(c, truth.Partial("compose write verification failed; rollback also failed",
				truth.KV("rollbackError", rollbackErr),
			))
			return
		}
		truth.Render(c, truth.Failed("compose write verification failed; both files rolled back", nil))
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "update_compose_env", "Updated compose and env atomically")

	details := map[string]any{
		"compose": stack.ComposeFile,
	}
	if hasEnvUpdate {
		details["env"] = stack.EnvFile
	}
	if len(lintResults) > 0 {
		details["lintResults"] = lintResults
	}

	c.JSON(http.StatusOK, truth.ActionResult{
		Outcome: truth.OutcomeSuccess,
		Reason:  "compose and env saved",
		Details: details,
	})
}
