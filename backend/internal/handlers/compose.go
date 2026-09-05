package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
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

	// nil arm dropped, dead per GetStack's return shape (database/stacks.go:42-53
	// always returns either &stack or a non-nil err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrStackNotFound,
				"Stack not found",
			))
			return
		}
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load stack", err))
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

	//nolint:gosec // composePath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "READ_ERROR", "Failed to read compose file", err))
		return
	}

	fileInfo, err := os.Stat(composePath)
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "STAT_ERROR", "Failed to get file info", err))
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

	// nil arm dropped, dead per GetStack's return shape (database/stacks.go:42-53
	// always returns either &stack or a non-nil err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrStackNotFound,
				"Stack not found",
			))
			return
		}
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load stack", err))
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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "LINT_ERROR", "Failed to lint compose file", err))
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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "WRITE_ERROR", "Failed to write compose file", err))
		return
	}

	h.logAction(c, id, "update_compose", "Updated compose file: "+stack.ComposeFile)

	c.JSON(http.StatusOK, ComposeSaveResponse{
		Saved:       true,
		LintResults: lintResults,
	})
}

func (h *ComposeHandler) Lint(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// agent-os-7lg1 judgement call: Lint works standalone — a genuine DB
		// fault here only degrades the working directory used for linting
		// (falls back to /tmp below), so it is worth an operator-visible log
		// line but not worth failing a lint request that never required the
		// stack to exist. Unlike the other 13 db.GetStack sites in this
		// package, there is no 404 arm here to preserve: this call never
		// returned one before, and still doesn't.
		slog.Error("failed to load stack for compose lint", "stack_id", id, "error", err)
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

	workingDir := "/tmp"
	if stack != nil {
		workingDir = stack.Directory
	}

	lintResults, err := h.linter.LintWithDir(req.Content, workingDir)
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "LINT_ERROR", "Failed to lint compose file", err))
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

func (h *ComposeHandler) logAction(c *gin.Context, stackID, action, detail string) {
	h.actionLog.LogWithRequest(middleware.RequestIDFrom(c), userIDFrom(c), &stackID, action, detail)
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
	// Best-effort restore of original content via the same atomic 0600 writer
	// used for the primary write below and by env.go's Put, so there is one
	// implementation that must stay in sync with the 0600 mode requirement
	// instead of two that can drift apart (agent-os-i94).
	//nolint:errcheck // best-effort restore; nothing further to do on failure
	_ = writeEnvFileAtomic(envPath, string(originalBytes))
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

	// nil arm dropped, dead per GetStack's return shape (database/stacks.go:42-53
	// always returns either &stack or a non-nil err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrStackNotFound,
				"Stack not found",
			))
			return
		}
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load stack", err))
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
			renderResult(c, truth.Failed("env validation failed: "+err.Error(), err))
			return
		}
		envContent = serializeEnvFile(req.EnvEntries)
		hasEnvUpdate = true
	}

	// This endpoint writes the .env file too, so it is a second door onto the
	// same file EnvHandler.Put guards — gating only that one would leave the
	// bypass wide open. Conditional on hasEnvUpdate: a compose-only save touches
	// no secret and needs no second factor.
	if hasEnvUpdate && !envUnlocked(c) {
		c.JSON(http.StatusForbidden, models.NewAppError(
			http.StatusForbidden,
			models.ErrForbidden,
			"Re-enter your password to edit environment variables",
		))
		return
	}

	// ── Lint compose before touching disk ──────────────────────────────────
	lintResults, err := h.linter.LintWithDir(req.ComposeContent, stack.Directory)
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "LINT_ERROR", "Failed to lint compose file", err))
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
	//nolint:gosec // composePath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
	originalCompose, readErr := os.ReadFile(composePath)
	if readErr != nil && !os.IsNotExist(readErr) {
		renderResult(c, truth.Failed("failed to read existing compose file for rollback snapshot", readErr))
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
		//nolint:gosec // envPath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
		if snapshot, rerr := os.ReadFile(envPath); rerr == nil {
			originalEnv = snapshot
		} else if !os.IsNotExist(rerr) {
			renderResult(c, truth.Failed("failed to read existing env file for rollback snapshot", rerr))
			return
		}

		// Write via the same atomic 0600 writer env.go's Put uses (agent-os-i94)
		// instead of a hand-rolled WriteFile+Rename at 0644 — the previous
		// version here downgraded the file's mode on every save because
		// os.Rename replaces the destination inode wholesale.
		if err := writeEnvFileAtomic(envPath, envContent); err != nil {
			renderResult(c, truth.Failed("failed to write env file; compose unchanged", err))
			return
		}

		// Verify round-trip for env.
		if ar := verifyEnvRoundTrip(envPath, envContent); ar != nil {
			// .env written but verification failed — restore original bytes.
			restoreEnv(envPath, originalEnv)
			renderResult(c, *ar)
			return
		}
	}

	// ── Write compose.yaml ──────────────────────────────────────────────────
	if err := os.WriteFile(composePath, []byte(req.ComposeContent), 0644); err != nil {
		// Compose write failed after .env was already replaced — restore .env.
		if hasEnvUpdate && envPath != "" {
			restoreEnv(envPath, originalEnv)
		}
		renderResult(c, truth.Failed("failed to write compose file; env rolled back", err))
		return
	}

	// ── Verify compose was written ─────────────────────────────────────────
	//nolint:gosec // composePath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
	if writtenCompose, rerr := os.ReadFile(composePath); rerr != nil || string(writtenCompose) != req.ComposeContent {
		// Compose verification failed — roll back both files.
		rollbackErr := ""
		if originalCompose != nil {
			//nolint:gosec // composePath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
			if rErr := os.WriteFile(composePath, originalCompose, 0644); rErr != nil {
				rollbackErr = rErr.Error()
			}
		}
		if hasEnvUpdate && envPath != "" {
			restoreEnv(envPath, originalEnv)
		}
		if rollbackErr != "" {
			renderResult(c, truth.Partial("compose write verification failed; rollback also failed",
				truth.KV("rollbackError", rollbackErr),
			))
			return
		}
		renderResult(c, truth.Failed("compose write verification failed; both files rolled back", nil))
		return
	}

	h.logAction(c, id, "update_compose_env", "Updated compose and env atomically")

	details := map[string]any{
		"compose": stack.ComposeFile,
	}
	if hasEnvUpdate {
		details["env"] = stack.EnvFile
	}
	if len(lintResults) > 0 {
		details["lintResults"] = lintResults
	}

	renderResultWithStatus(c, http.StatusOK, truth.ActionResult{
		Outcome: truth.OutcomeSuccess,
		Reason:  "compose and env saved",
		Details: details,
	})
}
