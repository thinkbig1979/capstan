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
	"github.com/thinkbig1979/capstan/backend/internal/services"
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

	// The stack ID is fully determined by targetDir and req.Name, so it is minted
	// here — before anything touches the filesystem — and used as this create's
	// operation-lock key.
	//
	// The lock MUST be taken before the os.Stat guard below, not after it.
	// Placing it after (the natural spot, since that is where the duplicate check
	// lives) still loses the race: both requests clear the stat, the loser then
	// waits, and resumes holding a stale stat result — overwriting the winner's
	// compose file while still reporting 201. Holding the lock across the stat
	// means the loser either fails to acquire (409 here) or re-runs the stat with
	// the winner's directory already on disk (409 below), the same answer either
	// way (agent-os-0br).
	//
	// Keying on the stack ID rather than on stackDir puts Create in the same
	// keyspace as Delete and the lifecycle handlers, so a create and a delete of
	// the same stack interlock, while two creates of DIFFERENT names never
	// contend. That distinction matters because Acquire is a try-lock that fails
	// fast rather than queueing: a broader key (the target directory, or a global
	// one) would turn unrelated concurrent creates into spurious conflicts.
	// Two configured roots sharing a basename used to mint the SAME id, which
	// made this lock key collide too: concurrent same-name creates into two such
	// roots conflicted with each other and one caller got a spurious 409. They
	// now get distinct ids, so they no longer share a key (agent-os-elo).
	//
	// services.StackID is shared with the scanner, which must derive the same id
	// for this stack when it next walks the disk. targetDir is passed as an
	// already-resolved root: isValidStacksDir accepted it only by exact match
	// against a configured root.
	stackID := services.StackID(targetDir, h.config.StacksDir, h.config.ExtraStacksDirs, req.Name, "default")

	// DUPLICATE_STACK rather than a new code: losing this race is the same
	// user-visible situation as the sequential duplicate below — the name is
	// taken — and the sequential path already answers 409 DUPLICATE_STACK for it.
	if _, err := h.opLock.Acquire(stackID); err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrDuplicateStack,
			fmt.Sprintf("Stack '%s' is already being created or modified by another operation", req.Name),
		))
		return
	}
	defer h.opLock.Release(stackID)

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

	projectName := fmt.Sprintf("%s-default", req.Name)

	stack := models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     envFile,
		ProjectName: projectName,
		Status:      "stopped",
	}

	// stacks.directory has an FK to directories(path) (migrations.go), enforced
	// pool-wide since e4e8c3f. stackDir is a brand-new subdirectory the scanner
	// has never indexed (the UI's DirectorySelect only offers already-monitored
	// PARENT roots, so this is the common case, not an edge case), so no
	// directories row exists for it yet. Register it before UpsertStack or every
	// such Create 500s with a FK violation (agent-os-jcu).
	registeredDir, err := h.scanner.RegisterDirectory(stackDir, targetDir)
	if err != nil {
		os.RemoveAll(stackDir)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"DB_ERROR",
			"Failed to register stack directory in database",
		))
		return
	}

	if err := h.db.UpsertStack(stack); err != nil {
		// Roll back the directory registration too, so a failed create never
		// leaves a directories row with no stack behind it — but ONLY when this
		// request is the one that inserted that row.
		//
		// UnregisterDirectory cascades: stacks.directory is an FK onto
		// directories.path with ON DELETE CASCADE (migrations.go), so removing
		// the row removes every stacks row sharing that exact directory, and one
		// directory legitimately holds several stacks (one per compose file).
		// The os.Stat guard above does not protect us here — it is a filesystem
		// check, and a directories row can outlive its directory: Delete removes
		// the directory from disk and its own stacks row but never unregisters
		// the directory, so a re-Create of that path sails past the 409 with
		// siblings still registered underneath.
		//
		// registeredDir comes from the INSERT's own RowsAffected rather than a
		// read-before-write, so a transient DB error cannot masquerade as
		// "absent" and re-arm the cascade.
		//
		// The in-process concurrent case is now covered too: Create holds the
		// operation lock for this stack ID from before the os.Stat guard through
		// this rollback, so a second Create for the same name cannot be inside
		// this block at the same time (agent-os-0br). The lock is per-process, so
		// an external writer or a second replica still can be.
		if registeredDir {
			if unregErr := h.scanner.UnregisterDirectory(stackDir); unregErr != nil {
				slog.Warn("failed to roll back directory registration after UpsertStack failure",
					"directory", stackDir, "error", unregErr)
			}
		}
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

	h.logAction(c, stackID, "create", fmt.Sprintf("Created new stack: %s", req.Name))

	// req.Deploy is false: stack created successfully, no deploy requested.
	//
	// Deliberately no `h.docker == nil` term here: main.go passes the concrete
	// *services.DockerService, so a Docker outage leaves a nil pointer inside a
	// non-nil interface and that test never fires. The deploy attempt below
	// refuses on its own and reports "created but not deployed: Docker daemon
	// unreachable", which tells the operator more than silently skipping it
	// would (agent-os-xay).
	if !req.Deploy {
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
	deployAR, deployOutput := h.dockerSvc().StartVerified(stack)
	h.logAction(c, stackID, "start", deployOutput)

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
			models.ErrOperationInProgress,
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
	deleteAR, deleteOutput := h.dockerSvc().DeleteVerified(*stack)

	h.logAction(c, id, "delete", deleteOutput)

	if deleteAR.Outcome != truth.OutcomeSuccess && deleteAR.Outcome != truth.OutcomeNoChange {
		renderDockerResult(c, deleteAR.Err, truth.ActionResult{
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

	// Remove the stack's files from disk. One directory legitimately holds
	// several stacks (one per compose file — IDs are root~path:project), so
	// removing the whole directory would destroy every sibling stack's compose
	// file while their containers keep running: DeleteVerified composes down only
	// this stack's project, and their stacks rows survive the delete.
	if rmErr := h.removeStackFiles(*stack, absStackDir); rmErr != nil {
		renderResult(c, truth.Failed("stack compose down succeeded but file removal failed", rmErr,
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

// removeStackFiles deletes from disk only what belongs to stack. When no other
// stack is still registered under its directory the whole directory goes, which
// is the single-stack case and matches the behaviour before siblings were
// considered. When siblings remain, only this stack's own compose file (and env
// file, if no survivor shares it) is removed and the directory stays.
//
// absStackDir has already been proven inside a configured stacks root by
// Delete's path-traversal guard; every per-file path derived here is proven
// inside absStackDir by the same helper, so a malformed compose_file or env_file
// value cannot reach outside the stack's own directory.
func (h *StacksHandler) removeStackFiles(stack models.Stack, absStackDir string) error {
	registered, err := h.db.ListStacksByDirectory(stack.Directory)
	if err != nil {
		// Without the sibling list we cannot tell a sole stack from one of
		// several, and guessing wrong destroys another stack's files. Refuse.
		return fmt.Errorf("failed to list stacks registered under %s: %w", stack.Directory, err)
	}

	survivors := make([]models.Stack, 0, len(registered))
	for _, s := range registered {
		if s.ID != stack.ID {
			survivors = append(survivors, s)
		}
	}

	if len(survivors) == 0 {
		return os.RemoveAll(absStackDir)
	}

	if err := removeStackFile(absStackDir, stack.ComposeFile); err != nil {
		return err
	}

	// The env file goes only when no survivor references the same one:
	// determineEnvFile (services/scanner.go) falls back to .env for a named stack
	// that has no .env.<name>, so two stacks legitimately share one env file.
	for _, s := range survivors {
		if s.EnvFile != "" && s.EnvFile == stack.EnvFile {
			return nil
		}
	}
	return removeStackFile(absStackDir, stack.EnvFile)
}

// removeStackFile removes name from absStackDir after proving the resolved path
// is inside it. An absent file is not an error: a stack row can outlive the file
// it points at. os.Remove rather than os.RemoveAll, so a name that resolves to a
// directory cannot take a subtree with it.
func removeStackFile(absStackDir, name string) error {
	if name == "" {
		return nil
	}

	absFile, err := filepath.Abs(filepath.Join(absStackDir, name))
	if err != nil {
		return fmt.Errorf("failed to resolve %q inside %s: %w", name, absStackDir, err)
	}
	if !stackDirIsInsideRoot(absFile, absStackDir) {
		return fmt.Errorf("refusing to remove %q: it resolves outside the stack directory %s", name, absStackDir)
	}
	if err := os.Remove(absFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
