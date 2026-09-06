package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	// The charset above admits "---" and "_._", from which Docker Compose
	// derives an EMPTY project name and then refuses every -p (agent-os-f3ah).
	// Refuse it here, naming the rule, rather than persisting a row that can
	// never start. Same producer as the row below, so the check cannot drift
	// from what gets stored.
	if services.ComposeProjectName(req.Name, "default") == "" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Stack name must contain at least one letter or digit: Docker Compose derives the project name from it by keeping only lowercase letters, digits, '-' and '_' and trimming leading '-' and '_'",
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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve target directory", err))
		return
	}

	absStackDir, err := filepath.Abs(stackDir)
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve stack directory", err))
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
				"lintResults": lintResults,
			},
		))
		return
	}

	if err := os.MkdirAll(stackDir, 0755); err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "MKDIR_ERROR", "Failed to create stack directory", err))
		return
	}

	composePath := filepath.Join(stackDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(req.ComposeContent), 0644); err != nil {
		os.RemoveAll(stackDir)
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "WRITE_ERROR", "Failed to write compose file", err))
		return
	}

	envFile := ""
	if req.EnvContent != "" {
		envPath := filepath.Join(stackDir, ".env")
		if err := os.WriteFile(envPath, []byte(req.EnvContent), 0600); err != nil {
			os.RemoveAll(stackDir)
			handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "WRITE_ERROR", "Failed to write env file", err))
			return
		}
		envFile = ".env"
	}

	// Same producer the scanner uses, for the same reason stackID above comes
	// from services.StackID: the scan at the end of this handler upserts over
	// this row, and the deploy below runs with whatever is in `stack`. Deriving
	// the name here independently is what let the two disagree (agent-os-07x).
	// "default" is the compose profile for the compose.yaml written above.
	//
	// It reads composePath, which exists: the request body was written to it
	// above, before this point, so the create asks compose the same question
	// about the same bytes the scan will ask minutes later. A body carrying a
	// top-level `name:` is therefore adopted here and NOT rewritten out from
	// under the running containers by the next scan (agent-os-89z2).
	projectName, _ := services.ResolveComposeProjectName(c.Request.Context(), composePath, req.Name, "default")

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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "DB_ERROR", "Failed to register stack directory in database", err))
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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "DB_ERROR", "Failed to register stack in database", err))
		return
	}

	if err := h.scanner.ScanDirectoryWithRoot(stackDir, targetDir); err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "SCANNER_ERROR", "Failed to scan new stack directory", err))
		return
	}

	// Re-read what the scan just persisted, and respond with THAT.
	//
	// The scan above is synchronous and upserts over the row written at
	// UpsertStack, filling in the fields only the scanner produces —
	// IsGitRepo and GitBranch (services/scanner.go is their sole writer;
	// no handler ever sets them). The struct literal above therefore still
	// holds their zero values, and rendering it reported a stack in a
	// perfectly healthy git-backed directory as not a git repo at all, for
	// one response, while the database said otherwise. The database was
	// never wrong here and no later scan had to repair it: only the response
	// disagreed with the row the same handler had just written
	// (agent-os-hgtb).
	//
	// Fixed by re-reading rather than by populating the literal above,
	// which would duplicate the git resolution the scanner already does 59
	// lines later and re-create the two-producers-disagreeing defect
	// agent-os-07x fixed — the same reason ResolveComposeProjectName is
	// called there instead of deriving the name independently.
	//
	// Done here, BEFORE the deploy branch, so all three response paths
	// (created, created+deployed, created-but-not-deployed) report the
	// persisted row rather than only the first of them. StartVerified below
	// then deploys from the row too: the comment above exists precisely so
	// the local and the persisted row agree, and where they ever did not,
	// the row is what every other part of the system reads.
	if persisted, err := h.db.GetStack(stackID); err != nil {
		// Deliberately NOT fatal, and not the fail-open this codebase has
		// been closing elsewhere: the outcome is unaffected. The stack WAS
		// created and persisted — directory, database row and scan all
		// succeeded — and only the freshness of this response's decoration
		// is in question. Answering a successful create with a 500 would
		// report a false outcome, which is the defect class, not an
		// instance of it. So: log the cause AND the consequence, and serve
		// the pre-scan values.
		slog.Warn("could not re-read the newly created stack after its scan; the git fields in this response may be stale, though the stored row is correct",
			"id", stackID, "error", err)
	} else {
		stack = *persisted
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
		renderResultWithStatus(c, http.StatusCreated, truth.ActionResult{
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
		renderResultWithStatus(c, http.StatusCreated, truth.ActionResult{
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
		renderResultWithStatus(c, http.StatusMultiStatus, truth.ActionResult{
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

	// This survivor check is for the collateral PROMPT only — it must not be
	// reused to decide the destructive branch below. DeleteVerified (compose
	// down -v) runs between here and removeStackFiles and can take seconds, long
	// enough for a directory-watcher scan or a concurrent Create to register a
	// new sibling under this same directory. removeStackFiles re-queries for
	// itself immediately before it acts, so that decision is always made on
	// fresh data; a stale answer here is harmless, since the worst case is an
	// unnecessary prompt or a skipped one followed by the (always safe) per-file
	// removal path.
	survivorsBeforePrompt, survErr := h.listSurvivors(*stack)
	if survErr != nil {
		renderResult(c, truth.Failed("failed to list stacks registered under stack directory", survErr,
			truth.KV("id", id),
			truth.KV("directory", stack.Directory),
		))
		return
	}

	// When stack is the sole one registered under its directory, deleting it
	// removes the whole directory (removeStackFiles below) rather than just its
	// own compose/env file. Anything else sitting there — a bind-mounted data
	// directory, .git, operator notes — goes with it. Refuse and enumerate what
	// would be destroyed before touching containers or files, unless the caller
	// has explicitly acknowledged the loss. When a sibling survives, the
	// per-file removal path never touches anything but this stack's own two
	// files, so nothing here is ever at risk and no prompt is needed.
	if len(survivorsBeforePrompt) == 0 {
		collateral, colErr := collateralEntries(*stack, absStackDir)
		if colErr != nil {
			renderResult(c, truth.Failed("failed to inspect stack directory for non-stack files", colErr,
				truth.KV("id", id),
				truth.KV("directory", stack.Directory),
			))
			return
		}

		if len(collateral) > 0 && c.Query("confirmCollateral") != "true" {
			c.JSON(http.StatusPreconditionRequired, models.NewAppErrorWithDetails(
				http.StatusPreconditionRequired,
				"STACK_DELETE_COLLATERAL",
				"Deleting this stack will also remove other files in its directory; add ?confirmCollateral=true to proceed",
				map[string]any{
					"directory":  stack.Directory,
					"collateral": collateral,
				},
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

	// Clean up the directories row too, but only once it is actually orphaned:
	// this MUST run after DeleteStack above, never before — otherwise the
	// stack row being deleted is itself still counted as a reference and the
	// guard can never fire. DeleteDirectoryIfOrphaned re-checks for surviving
	// siblings atomically in SQL (see its doc comment), so it is safe even
	// though a concurrent Create could have registered a new sibling here
	// since removeStackFiles' own survivor check above.
	//
	// This is best-effort: the stack itself is already fully deleted (files,
	// containers, and its own row) by this point, so a failure here must not
	// fail the request — it would just leave the orphan for the next Rescan
	// (pruneStaleStacks) to clean up, same as before this fix existed.
	if _, dirErr := h.db.DeleteDirectoryIfOrphaned(stack.Directory); dirErr != nil {
		slog.Warn("failed to clean up orphaned directory row after stack delete",
			"directory", stack.Directory, "error", dirErr)
	}

	renderResult(c, truth.Success("stack deleted",
		truth.KV("id", id),
		truth.KV("output", deleteOutput),
	))
}

// listSurvivors returns the other stacks still registered under stack's
// directory (every row under the same directory except stack.ID itself). Both
// the collateral check and removeStackFiles need to know whether stack is the
// sole one registered there, so Delete computes this once and threads it
// through rather than querying the DB twice.
func (h *StacksHandler) listSurvivors(stack models.Stack) ([]models.Stack, error) {
	registered, err := h.db.ListStacksByDirectory(stack.Directory)
	if err != nil {
		// Without the sibling list we cannot tell a sole stack from one of
		// several, and guessing wrong destroys another stack's files. Refuse.
		return nil, fmt.Errorf("failed to list stacks registered under %s: %w", stack.Directory, err)
	}

	survivors := make([]models.Stack, 0, len(registered))
	for _, s := range registered {
		if s.ID != stack.ID {
			survivors = append(survivors, s)
		}
	}
	return survivors, nil
}

// collateralEntries lists what a sole-stack delete would destroy beyond the
// stack's own files: everything in absStackDir other than stack.ComposeFile
// and stack.EnvFile — bind-mounted data directories, .git, operator notes.
// It only matters when stack has no surviving sibling (removeStackFiles takes
// the whole directory in that case); when a sibling remains, removeStackFiles
// never touches anything but the stack's own two files, so nothing here is
// ever at risk. Returned names are basenames, not full paths — the caller
// already has absStackDir / stack.Directory if it needs the rest.
func collateralEntries(stack models.Stack, absStackDir string) ([]string, error) {
	entries, err := os.ReadDir(absStackDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read stack directory %s: %w", absStackDir, err)
	}

	own := map[string]bool{}
	if stack.ComposeFile != "" {
		own[stack.ComposeFile] = true
	}
	if stack.EnvFile != "" {
		own[stack.EnvFile] = true
	}

	var collateral []string
	for _, e := range entries {
		if own[e.Name()] {
			continue
		}
		collateral = append(collateral, e.Name())
	}
	sort.Strings(collateral)
	return collateral, nil
}

// removeStackFiles deletes from disk only what belongs to stack. When no other
// stack is still registered under its directory the whole directory goes, which
// is the single-stack case and matches the behaviour before siblings were
// considered — Delete has already required acknowledgement of any collateral
// that removal would take with it. When siblings remain, only this stack's own
// compose file (and env file, if no survivor shares it) is removed and the
// directory stays.
//
// absStackDir has already been proven inside a configured stacks root by
// Delete's path-traversal guard; every per-file path derived here is proven
// inside absStackDir by the same helper, so a malformed compose_file or env_file
// value cannot reach outside the stack's own directory.
//
// This re-queries listSurvivors itself rather than accepting Delete's earlier
// result: DeleteVerified (compose down -v) runs between Delete's collateral
// prompt and this call and can take seconds, long enough for a directory-watcher
// scan or a concurrent Create to register a new sibling here. The destructive
// os.RemoveAll branch must be chosen on the freshest possible answer — reusing a
// pre-compose-down capture reintroduces the agent-os-xa7 data loss (destroying a
// newly-registered sibling's compose file) through a widened race instead of
// through the logic xa7 already guards against.
func (h *StacksHandler) removeStackFiles(stack models.Stack, absStackDir string) error {
	survivors, err := h.listSurvivors(stack)
	if err != nil {
		return err
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
