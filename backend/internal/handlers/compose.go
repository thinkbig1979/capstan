package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

type ComposeHandler struct {
	linter    *services.LinterService
	db        *database.DB
	config    *config.Config
	actionLog *services.ActionLogger
	// opLock serialises file writes with every other operation on the stack
	// (agent-os-a1ye.4). Set by SetOperationLock; nil (tests) means no locking.
	opLock *services.OperationLock
}

type ComposeRequest struct {
	Content string `json:"content" binding:"required"`
}

func NewComposeHandler(linter *services.LinterService, db *database.DB, config *config.Config) *ComposeHandler {
	return &ComposeHandler{
		linter:    linter,
		db:        db,
		config:    config,
		actionLog: services.NewActionLogger(db),
	}
}

// SetOperationLock installs the per-stack operation lock shared with the
// lifecycle, update and backup paths. main.go passes the same instance.
func (h *ComposeHandler) SetOperationLock(l *services.OperationLock) {
	h.opLock = l
}

func (h *ComposeHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/:id/compose", h.Get)
	group.PUT("/:id/compose", h.Put)
	group.POST("/:id/compose/lint", h.Lint)
	group.PUT("/:id/compose-env", h.PutComposeAndEnv)
}

func (h *ComposeHandler) Get(c *gin.Context) {
	id := c.Param("id")

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
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

	resp := ComposeResponse{
		Content:      string(content),
		Filename:     stack.ComposeFile,
		Size:         fileInfo.Size(),
		LastModified: fileInfo.ModTime().Format(time.RFC3339),
		Locked:       !envUnlocked(c),
	}
	// A session that has not unlocked env values must not read the secrets
	// the env views hide from it (agent-os-w6wx).
	if resp.Locked {
		resp.Content, resp.MaskedValues = services.MaskSecrets(resp.Content, stack.ComposeFile, h.stackSecrets(*stack))
	}
	c.JSON(http.StatusOK, resp)
}

// stackSecrets are the stack's env values, masked wherever a locked view
// shows them verbatim.
func (h *ComposeHandler) stackSecrets(stack models.Stack) []string {
	return services.StackSecretValues(stack, globalEnvPath(h.config))
}

// refuseMaskedSave answers and returns true when this save of content must
// not run, because it would write a masked "***" over a secret
// (agent-os-w6wx). Both checks read the file on disk, not the request's
// claims, so a stale or forged client cannot get past them:
//
//   - A session that has not unlocked env values gets 403 when the file
//     holds a value the GET masked for it: what its editor holds is "***"
//     where a secret was.
//   - An unlocked session gets 409 when a line of content equals the masked
//     form of a line on disk that the masker changed: the editor holds text
//     from a locked view (an undo back past an unlock did that), and saving
//     it would overwrite the secret.
//
// A link the write would refuse answers that refusal (409); any other
// failure to read the file refuses too: fail closed.
func (h *ComposeHandler) refuseMaskedSave(c *gin.Context, stack *models.Stack, composePath, content string) bool {
	unlocked := envUnlocked(c)
	if unlocked && !strings.Contains(content, services.SecretMask) {
		return false // nothing masked can be in it
	}
	target, info, err := resolveWriteTarget(composePath)
	if err != nil {
		handleError(c, writeFailureError(err, "READ_ERROR", "Failed to read the compose file on disk"))
		return true
	}
	if info == nil {
		return false // nothing on disk yet, nothing to overwrite
	}
	//nolint:gosec // target is the compose path resolved by resolveWriteTarget; only masked lines are compared, nothing of it is returned
	disk, err := os.ReadFile(target)
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "READ_ERROR", "Failed to read the compose file on disk", err))
		return true
	}
	masked, n := services.MaskSecrets(string(disk), stack.ComposeFile, h.stackSecrets(*stack))
	if !unlocked {
		if n > 0 {
			c.JSON(http.StatusForbidden, models.NewAppError(
				http.StatusForbidden,
				models.ErrForbidden,
				"Re-enter your password to edit a compose file that holds secrets",
			))
			return true
		}
		return false
	}
	if n > 0 && holdsMaskedLine(content, string(disk), masked) {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrValidation,
			"This save holds masked values (***) from a locked view and would overwrite the secrets they hide; reload the file and try again",
		))
		return true
	}
	return false
}

// holdsMaskedLine reports whether a line of content equals the masked form of
// a line the masker changed in disk (masked is disk masked, line for line). A
// masked form that is also a line of disk as written is not counted: the
// file really holds it.
func holdsMaskedLine(content, disk, masked string) bool {
	diskLines, maskedLines := strings.Split(disk, "\n"), strings.Split(masked, "\n")
	if len(diskLines) != len(maskedLines) {
		return true // MaskSecrets keeps the line count; fail closed if not
	}
	raw := make(map[string]bool, len(diskLines))
	for _, l := range diskLines {
		raw[l] = true
	}
	forms := map[string]bool{}
	for i, l := range maskedLines {
		if l != diskLines[i] && !raw[l] {
			forms[l] = true
		}
	}
	for _, l := range strings.Split(content, "\n") {
		if forms[l] {
			return true
		}
	}
	return false
}

func (h *ComposeHandler) Put(c *gin.Context) {
	id := c.Param("id")

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
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

	composePath := filepath.Join(stack.Directory, stack.ComposeFile)

	// Held from before the lint (which reads the stack directory) to after
	// the write, with the lock of every stack sharing the file (agent-os-0j8a).
	releaseLock, ok := lockStacksSharingFiles(c, h.opLock, h.db, stack, services.OpKindCompose, composePath)
	if !ok {
		return
	}
	defer releaseLock()

	// Before the lint: a refused save answers 403 or 409, whatever the lint
	// would say.
	if h.refuseMaskedSave(c, stack, composePath, req.Content) {
		return
	}

	lintResults, err := h.linter.LintCompose(req.Content, stack.Directory, services.StackLintEnv(*stack, globalEnvPath(h.config)), envUnlocked(c))
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "LINT_ERROR", "Failed to lint compose file", err))
		return
	}

	if lintHasErrors(lintResults) {
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

	if err := validateStackPath(composePath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid compose file path",
		))
		return
	}

	if err := writeComposeFileAtomic(composePath, []byte(req.Content)); err != nil {
		handleError(c, writeFailureError(err, "WRITE_ERROR", "Failed to write compose file"))
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
	if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
		// agent-os-7lg1 judgement call: Lint works standalone — a genuine DB
		// fault here only degrades the working directory used for linting
		// (none, below), so it is worth an operator-visible log
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

	// No stack row: lint as for a stack that has no folder yet, with
	// global.env alone.
	workingDir := ""
	env := services.NewLintEnv(globalEnvPath(h.config), "")
	if stack != nil {
		workingDir = stack.Directory
		env = services.StackLintEnv(*stack, globalEnvPath(h.config))
	}

	lintResults, err := h.linter.LintCompose(req.Content, workingDir, env, envUnlocked(c))
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "LINT_ERROR", "Failed to lint compose file", err))
		return
	}

	c.JSON(http.StatusOK, LintResponse{
		Valid:       !lintHasErrors(lintResults),
		LintResults: lintResults,
	})
}

// lintHasErrors is the one save gate on lint results: any error-level result
// refuses the save (or reports the content invalid). Warnings never do.
func lintHasErrors(results []models.LintResult) bool {
	for _, r := range results {
		if r.Level == "error" {
			return true
		}
	}
	return false
}

// globalEnvPath is global.env under the data directory, "" without one, so a
// test config with no DataDir never reads a global.env from the working
// directory.
func globalEnvPath(cfg *config.Config) string {
	if cfg == nil || cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(cfg.DataDir, "global.env")
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
	_ = writeEnvFileAtomic(envPath, string(originalBytes)) //writerefusal:ignore best-effort restore, nothing is rendered
}

// ComposeEnvRequest is the body for the atomic PUT /api/v1/stacks/:id/compose-env
// endpoint. Each file is replaced by temp-file-plus-rename, and if either write
// fails both are rolled back so neither is left partially changed (#11). The
// pair is not one atomic step: a crash between the two renames leaves the new
// env beside the old compose.
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
//  3. Only write compose.yaml (also temp file plus rename) after .env rename succeeded.
//  4. On any failure after the .env rename: restore the original compose file from
//     a pre-write backup byte slice (taken before step 3). If the restore fails,
//     the response is partial rather than false success.
func (h *ComposeHandler) PutComposeAndEnv(c *gin.Context) {
	id := c.Param("id")

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
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

	composePath := filepath.Join(stack.Directory, stack.ComposeFile)

	// A stack with no env file recorded gets a new ".env" (agent-os-4p86): the
	// request is "Extract to .env", and skipping its env half while saving the
	// compose half that now references ${VAR} lost the value.
	envFileName := stack.EnvFile
	if hasEnvUpdate && envFileName == "" {
		envFileName = ".env"
	}
	lockPaths := []string{composePath}
	if hasEnvUpdate {
		lockPaths = append(lockPaths, filepath.Join(stack.Directory, envFileName))
	}

	// Held across the lint, both writes, and the rollback paths, with the lock
	// of every stack sharing either file (agent-os-0j8a).
	releaseLock, ok := lockStacksSharingFiles(c, h.opLock, h.db, stack, services.OpKindCompose, lockPaths...)
	if !ok {
		return
	}
	defer releaseLock()

	// Before the lint: a refused save answers 403 or 409, whatever the lint
	// would say.
	if h.refuseMaskedSave(c, stack, composePath, req.ComposeContent) {
		return
	}

	// ── Lint compose before touching disk ──────────────────────────────────
	// The env half of this save is the env compose will read, so a variable
	// it sets counts as set.
	lintEnv := services.StackLintEnv(*stack, globalEnvPath(h.config))
	if hasEnvUpdate {
		lintEnv = lintEnv.WithEnvText(envContent)
	}
	lintResults, err := h.linter.LintCompose(req.ComposeContent, stack.Directory, lintEnv, envUnlocked(c))
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "LINT_ERROR", "Failed to lint compose file", err))
		return
	}
	if lintHasErrors(lintResults) {
		c.JSON(http.StatusUnprocessableEntity, models.NewAppErrorWithDetails(
			http.StatusUnprocessableEntity,
			models.ErrComposeValidation,
			"Compose file validation failed",
			gin.H{"lintResults": lintResults},
		))
		return
	}

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

	if hasEnvUpdate {
		envPath = filepath.Join(stack.Directory, envFileName)
		if err := validateStackPath(envPath, h.config); err != nil {
			c.JSON(http.StatusBadRequest, models.NewAppError(
				http.StatusBadRequest,
				models.ErrPathTraversal,
				"Invalid env file path",
			))
			return
		}

		// Only a .env that is truly absent is created. With none recorded, the
		// frontend sends just the one new VAR=value line (useExtractToEnv.ts),
		// so writing it over a .env the scan has not seen yet would replace
		// that file's other values. Any entry counts (file, symlink, dir), and
		// an unreadable answer refuses too: fail closed.
		if stack.EnvFile == "" {
			_, lerr := os.Lstat(envPath)
			switch {
			case lerr == nil:
				c.JSON(http.StatusConflict, models.NewAppError(
					http.StatusConflict,
					models.ErrValidation,
					"A .env file exists but is not linked to this stack; rescan the stack, then try again",
				))
				return
			case !os.IsNotExist(lerr):
				renderResult(c, truth.Failed("failed to check for an existing env file; nothing written", lerr))
				return
			}
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
			renderWriteFailure(c, err, "failed to write env file", "; compose unchanged")
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
	if err := writeComposeFileAtomic(composePath, []byte(req.ComposeContent)); err != nil {
		// Compose write failed after .env was already replaced — restore .env.
		note := ""
		if hasEnvUpdate && envPath != "" {
			restoreEnv(envPath, originalEnv)
			note = "; env rolled back"
		}
		renderWriteFailure(c, err, "failed to write compose file", note)
		return
	}

	// ── Verify compose was written ─────────────────────────────────────────
	//nolint:gosec // composePath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
	if writtenCompose, rerr := os.ReadFile(composePath); rerr != nil || string(writtenCompose) != req.ComposeContent { //geterrors:ignore read-back verification: "could not read it back" and "it differs from what we sent" both mean the write is unverified, and both take the same rollback
		// Compose verification failed — roll back both files.
		rollbackErr := ""
		if originalCompose != nil {
			//nolint:gosec // composePath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
			if rErr := writeComposeFileAtomic(composePath, originalCompose); rErr != nil { //writerefusal:ignore rollback; its text already goes to details.rollbackError
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

	// Record a .env this request created, so compose is handed it as
	// --env-file (services/docker.go passes it only when the stack row names
	// one). One column, as EnvHandler.Create does: the scanner does not take
	// this lock, so a whole-row write could undo its changes.
	if hasEnvUpdate && stack.EnvFile == "" {
		if _, err := h.db.SetStackEnvFileIfUnset(id, envFileName); err != nil {
			renderResult(c, truth.Partial("compose and env files saved but the stack was not updated to use the new env file",
				truth.KV("filename", envFileName),
				truth.KV("dbError", err.Error()),
			))
			return
		}
	}

	h.logAction(c, id, "update_compose_env", "Updated compose and env")

	details := map[string]any{
		"compose": stack.ComposeFile,
	}
	if hasEnvUpdate {
		details["env"] = envFileName
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
