package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

type CreateStackRequest struct {
	Name           string `json:"name" binding:"required"`
	Directory      string `json:"directory"`
	ComposeContent string `json:"composeContent" binding:"required"`
	EnvContent     string `json:"envContent"`
	Deploy         bool   `json:"deploy"`
}

func (h *StacksHandler) Create(c *gin.Context) {
	var req CreateStackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	if len(req.Name) < 1 || len(req.Name) > 50 {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Stack name must be between 1 and 50 characters",
		))
		return
	}

	matched, _ := regexp.MatchString(`^[a-zA-Z0-9._-]+$`, req.Name)
	if !matched {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Stack name must contain only alphanumeric characters, dots, underscores, and hyphens",
		))
		return
	}

	targetDir := h.config.StacksDir
	if req.Directory != "" {
		if !h.isValidStacksDir(req.Directory) {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				models.ErrValidation,
				"Invalid target directory",
			))
			return
		}
		targetDir = req.Directory
	}

	stackDir := filepath.Join(targetDir, req.Name)

	if _, err := os.Stat(stackDir); err == nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrDuplicateStack,
			fmt.Sprintf("Stack directory '%s' already exists", req.Name),
		))
		return
	}

	absTargetDir, err := filepath.Abs(targetDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to resolve target directory",
		))
		return
	}

	absStackDir, err := filepath.Abs(stackDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to resolve stack directory",
		))
		return
	}

	rel, err := filepath.Rel(absTargetDir, absStackDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid stack directory path",
		))
		return
	}

	lintResults, err := h.linter.Lint(req.ComposeContent)
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
				"lintResults": lintResults,
			},
		))
		return
	}

	if err := os.MkdirAll(stackDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"MKDIR_ERROR",
			"Failed to create stack directory",
		))
		return
	}

	composePath := filepath.Join(stackDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(req.ComposeContent), 0644); err != nil {
		os.RemoveAll(stackDir)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"WRITE_ERROR",
			"Failed to write compose file",
		))
		return
	}

	envFile := ""
	if req.EnvContent != "" {
		envPath := filepath.Join(stackDir, ".env")
		if err := os.WriteFile(envPath, []byte(req.EnvContent), 0600); err != nil {
			os.RemoveAll(stackDir)
			c.JSON(http.StatusInternalServerError, models.NewAppError(
				http.StatusInternalServerError,
				"WRITE_ERROR",
				"Failed to write env file",
			))
			return
		}
		envFile = ".env"
	}

	rootPrefix := filepath.Base(targetDir)
	stackID := fmt.Sprintf("%s~%s:default", rootPrefix, req.Name)
	projectName := fmt.Sprintf("%s-default", req.Name)

	stack := models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     envFile,
		ProjectName: projectName,
		Status:      "stopped",
	}

	if err := h.db.UpsertStack(stack); err != nil {
		os.RemoveAll(stackDir)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"DB_ERROR",
			"Failed to register stack in database",
		))
		return
	}

	if err := h.scanner.ScanDirectoryWithRoot(stackDir, targetDir); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"SCANNER_ERROR",
			"Failed to scan new stack directory",
		))
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), stackID, "create", fmt.Sprintf("Created new stack: %s", req.Name))

	// req.Deploy is false: stack created successfully, no deploy requested.
	if !req.Deploy || h.docker == nil {
		c.JSON(http.StatusCreated, truth.ActionResult{
			Outcome: truth.OutcomeSuccess,
			Reason:  "stack created",
			Details: map[string]any{
				"stack":       stack,
				"lintResults": lintResults,
				"deployed":    false,
			},
		})
		return
	}

	// Attempt deployment using StartVerified so the outcome reflects the actual
	// running state, not just compose exit code (finding #14).
	deployAR, deployOutput := h.docker.StartVerified(stack)
	h.logAction(userID.(string), stackID, "start", deployOutput)

	// Update DB with the verified status regardless of outcome.
	verifiedStatus := lifecycleStatus(deployAR)
	if err := h.db.UpdateStackStatus(stackID, verifiedStatus); err != nil {
		slog.Warn("failed to persist verified stack status", "id", stackID, "error", err)
	}
	stack.Status = verifiedStatus

	details := map[string]any{
		"stack":        stack,
		"lintResults":  lintResults,
		"deployOutput": deployOutput,
	}

	switch deployAR.Outcome {
	case truth.OutcomeSuccess:
		// Both create and deploy succeeded and are verified running.
		details["deployed"] = true
		c.JSON(http.StatusCreated, truth.ActionResult{
			Outcome: truth.OutcomeSuccess,
			Reason:  "stack created and deployed",
			Details: details,
		})

	default:
		// Stack was created and persisted (dir + DB + scan all succeeded).
		// Deploy failed or is partial — return 207 so the frontend knows to
		// invalidate ['stacks'] and badge the stack rather than discarding it.
		details["deployed"] = false
		details["deployError"] = deployAR.Reason
		if deployAR.Err != nil {
			details["deployError"] = deployAR.Err.Error()
		}
		c.JSON(http.StatusMultiStatus, truth.ActionResult{
			Outcome: truth.OutcomePartial,
			Reason:  "stack created but not deployed: " + deployAR.Reason,
			Details: details,
		})
	}
}

// stackDirIsInsideRoot returns true when absStackDir is strictly inside absRoot
// (i.e. it is a child path, not root itself). This is the path-traversal guard
// for Delete — we must never remove a directory outside the configured root.
func stackDirIsInsideRoot(absStackDir, absRoot string) bool {
	if absRoot == "" || absStackDir == "" {
		return false
	}
	// Ensure root ends with a separator so HasPrefix correctly rejects the root
	// directory itself (e.g. /stacks vs /stacks-evil).
	root := absRoot
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(absStackDir, root)
}

func (h *StacksHandler) Delete(c *gin.Context) {
	id := c.Param("id")

	if c.Query("confirm") != "true" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Confirmation required: add ?confirm=true to delete the stack",
		))
		return
	}

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	if _, err := h.opLock.Acquire(id); err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id)

	// Path-traversal guard: resolve the stack directory to an absolute path and
	// confirm it is strictly inside the configured stacks root before we attempt
	// any removal. This prevents a malformed stack record from deleting arbitrary
	// directories.
	absStackDir, pathErr := filepath.Abs(stack.Directory)
	if pathErr != nil {
		renderResult(c, truth.Failed("failed to resolve stack directory path", pathErr,
			truth.KV("id", id),
		))
		return
	}

	absStacksRoot, pathErr := filepath.Abs(h.config.StacksDir)
	if pathErr != nil {
		renderResult(c, truth.Failed("failed to resolve stacks root path", pathErr,
			truth.KV("id", id),
		))
		return
	}

	if !stackDirIsInsideRoot(absStackDir, absStacksRoot) {
		// Also accept paths inside any extra stacks dir configured by the operator.
		insideExtra := false
		for _, extra := range h.config.GetAllStacksDirs() {
			absExtra, aErr := filepath.Abs(extra)
			if aErr != nil {
				continue
			}
			if stackDirIsInsideRoot(absStackDir, absExtra) {
				insideExtra = true
				break
			}
		}
		if !insideExtra {
			renderResult(c, truth.Failed("stack directory is outside the configured stacks root; refusing to delete", nil,
				truth.KV("id", id),
				truth.KV("directory", stack.Directory),
			))
			return
		}
	}

	// Bring the stack down (compose down -v) and verify the containers are
	// actually gone before touching the filesystem or DB — a compose exit code
	// of 0 does not guarantee the containers stopped (same reasoning as
	// Start/Stop/Restart's verified lifecycle).
	deleteAR, deleteOutput := h.docker.DeleteVerified(*stack)

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "delete", deleteOutput)

	if deleteAR.Outcome != truth.OutcomeSuccess && deleteAR.Outcome != truth.OutcomeNoChange {
		renderResult(c, truth.ActionResult{
			Outcome: deleteAR.Outcome,
			Reason:  "compose down did not verify as removed: " + deleteAR.Reason,
			Details: mergeDetails(deleteAR.Details, map[string]any{
				"id":     id,
				"output": deleteOutput,
			}),
			Err: deleteAR.Err,
		})
		return
	}

	// Remove the stack directory from disk.
	if rmErr := os.RemoveAll(absStackDir); rmErr != nil {
		renderResult(c, truth.Failed("stack compose down succeeded but directory removal failed", rmErr,
			truth.KV("id", id),
			truth.KV("directory", stack.Directory),
		))
		return
	}

	// Remove the DB row. Surface any error rather than silently dropping it.
	if dbErr := h.db.DeleteStack(id); dbErr != nil {
		renderResult(c, truth.Failed("stack directory removed but DB delete failed", dbErr,
			truth.KV("id", id),
		))
		return
	}

	renderResult(c, truth.Success("stack deleted",
		truth.KV("id", id),
		truth.KV("output", deleteOutput),
	))
}
