package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// stackDocker is the subset of *services.DockerService that StacksHandler needs.
// It is declared consumer-side (here, not in services) so the handler depends on
// an abstraction it owns and can be exercised with a fake in unit tests, mirroring
// the dockerStopper pattern BackupService already uses. *services.DockerService
// satisfies it in production.
type stackDocker interface {
	GetStackStatuses(ctx context.Context, db services.DashboardDB) (map[string]services.LiveStatus, error)
	StartVerified(stack models.Stack) (truth.ActionResult, string)
	StopVerified(stack models.Stack) (truth.ActionResult, string)
	RestartVerified(stack models.Stack) (truth.ActionResult, string)
	PullVerified(stack models.Stack) (truth.ActionResult, string)
	DeleteVerified(stack models.Stack) (truth.ActionResult, string)
}

// stackStore is the subset of *database.DB that StacksHandler needs for stack
// persistence. GetStackByProjectName is included so a stackStore also satisfies
// services.DashboardDB, which GetStackStatuses requires. *database.DB satisfies it
// in production.
type stackStore interface {
	ListStacks() ([]models.Stack, error)
	GetStack(id string) (*models.Stack, error)
	GetStackByProjectName(projectName string) (*models.Stack, error)
	// ListStacksByDirectory is how Delete learns whether other stacks are still
	// registered under the directory it is about to remove files from.
	ListStacksByDirectory(path string) ([]models.Stack, error)
	UpsertStack(stack models.Stack) error
	UpdateStackStatus(id, status string) error
	DeleteStack(id string) error
	// DeleteDirectoryIfOrphaned is how Delete cleans up the directories row
	// left behind once no stack references it any longer, without risking a
	// live sibling's row (see the method's doc comment in database/stacks.go).
	DeleteDirectoryIfOrphaned(path string) (bool, error)
}

type StacksHandler struct {
	// docker is nil only when no Docker service was wired at all (tests). In
	// production main.go passes the concrete *services.DockerService, which is a
	// nil POINTER inside a non-nil interface during a Docker outage — so an
	// `h.docker != nil` test cannot detect one and must never be written here.
	// Always call through dockerSvc: DockerService guards its own nil receiver
	// and returns services.ErrDockerUnavailable, which the error branches
	// already handle (agent-os-xay).
	docker    stackDocker
	scanner   *services.ScannerService
	linter    *services.LinterService
	db        stackStore
	config    *config.Config
	actionLog *services.ActionLogger
	opLock    *services.OperationLock
}

func NewStacksHandler(docker stackDocker, scanner *services.ScannerService, linter *services.LinterService, db stackStore, cfg *config.Config, actionLog *services.ActionLogger, opLock *services.OperationLock) *StacksHandler {
	return &StacksHandler{
		docker:    docker,
		scanner:   scanner,
		linter:    linter,
		db:        db,
		config:    cfg,
		actionLog: actionLog,
		opLock:    opLock,
	}
}

// dockerSvc returns the Docker dependency, substituting a typed-nil
// *services.DockerService when none was wired at all.
//
// Calling a method on a nil INTERFACE panics, while calling one on a nil
// *services.DockerService dispatches to that type's nil-receiver guards and
// returns services.ErrDockerUnavailable (agent-os-xay). Converting the first
// shape into the second means the lifecycle handlers below refuse cleanly for
// both, without a nil check at every call site.
func (h *StacksHandler) dockerSvc() stackDocker {
	if h.docker == nil {
		return (*services.DockerService)(nil)
	}
	return h.docker
}

func (h *StacksHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("", h.Create)
	group.GET("", h.List)
	group.GET("/:id", h.Get)
	group.POST("/:id/start", h.Start)
	group.POST("/:id/stop", h.Stop)
	group.POST("/:id/restart", h.Restart)
	group.POST("/:id/pull", h.Pull)
	group.DELETE("/:id", h.Delete)
}

type LintRequest struct {
	Compose string `json:"compose" binding:"required"`
}

func (h *StacksHandler) Lint(c *gin.Context) {
	var req LintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	lintResults, err := h.linter.Lint(req.Compose)
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

	c.JSON(http.StatusOK, gin.H{
		"valid":       !hasErrors,
		"lintResults": lintResults,
	})
}

func (h *StacksHandler) List(c *gin.Context) {
	stacks, err := h.db.ListStacks()
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list stacks", err))
		return
	}

	// One ContainerList snapshot bucketed by compose project replaces the former
	// per-stack `docker compose ps` subprocess fan-out (O(1) Docker call instead
	// of O(N) process spawns). On snapshot error — including a Docker outage,
	// which arrives as services.ErrDockerUnavailable — we leave each stack's
	// stored DB status untouched (same graceful fallback as before).
	statuses, err := h.dockerSvc().GetStackStatuses(c.Request.Context(), h.db)
	if err != nil {
		slog.Error("Failed to derive live stack statuses", "error", err)
	} else {
		for i := range stacks {
			applyLiveStatus(&stacks[i], statuses)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"stacks": stacks,
	})
}

// composeUnreadable reports whether a stack's compose file can't be stat'd
// (missing/unreadable dir or file) — the condition that made `docker compose ps`
// return "unknown". Used to mark a container-less stack as "error" rather than
// "stopped" so a broken stack surfaces instead of hiding among intentionally
// stopped ones.
func composeUnreadable(s models.Stack) bool {
	_, err := os.Stat(filepath.Join(s.Directory, s.ComposeFile))
	return err != nil
}

// applyLiveStatus resolves a single stack against the shared container snapshot so
// List and Get agree. A project present in the snapshot takes its live status and
// reconstructed container list; a container-less project is "error" when its
// compose file is unreadable (what `docker compose ps` surfaced as "unknown") or
// "stopped" otherwise.
func applyLiveStatus(stack *models.Stack, statuses map[string]services.LiveStatus) {
	if ls, ok := statuses[stack.ProjectName]; ok && stack.ProjectName != "" {
		stack.Status = ls.Status
		stack.Containers = ls.Containers
		return
	}
	stack.Containers = []models.Container{}
	if composeUnreadable(*stack) {
		stack.Status = "error"
	} else {
		stack.Status = "stopped"
	}
}

func (h *StacksHandler) Get(c *gin.Context) {
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

	// Derive status from the same single-snapshot path List uses so a stack's
	// detail page agrees with its list row, instead of the old per-stack
	// `docker compose ps` subprocess (which returned "unknown" on error). On
	// snapshot failure we leave the stored DB status untouched, as List does.
	statuses, err := h.dockerSvc().GetStackStatuses(c.Request.Context(), h.db)
	if err != nil {
		slog.Error("Failed to derive live stack status", "error", err)
	} else {
		applyLiveStatus(stack, statuses)
	}

	c.JSON(http.StatusOK, stack)
}

func (h *StacksHandler) logAction(c *gin.Context, stackID, action, detail string) {
	h.actionLog.LogWithRequest(middleware.RequestIDFrom(c), userIDFrom(c), &stackID, action, detail)
}

func (h *StacksHandler) isValidStacksDir(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for _, stacksDir := range h.config.GetAllStacksDirs() {
		absStacksDir, err := filepath.Abs(stacksDir)
		if err != nil {
			continue
		}
		if absDir == absStacksDir {
			return true
		}
	}
	return false
}
