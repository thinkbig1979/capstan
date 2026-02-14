package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type StacksHandler struct {
	docker    *services.DockerService
	scanner   *services.ScannerService
	linter    *services.LinterService
	db        *database.DB
	config    *config.Config
	actionLog *services.ActionLogger
}

func NewStacksHandler(docker *services.DockerService, scanner *services.ScannerService, linter *services.LinterService, db *database.DB, cfg *config.Config) *StacksHandler {
	return &StacksHandler{
		docker:    docker,
		scanner:   scanner,
		linter:    linter,
		db:        db,
		config:    cfg,
		actionLog: services.NewActionLogger(db),
	}
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

type CreateStackRequest struct {
	Name           string `json:"name" binding:"required"`
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

	stackDir := filepath.Join(h.config.StacksDir, req.Name)

	if _, err := os.Stat(stackDir); err == nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			models.ErrDuplicateStack,
			fmt.Sprintf("Stack directory '%s' already exists", req.Name),
		))
		return
	}

	absStacksDir, err := filepath.Abs(h.config.StacksDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to resolve stacks directory",
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

	rel, err := filepath.Rel(absStacksDir, absStackDir)
	if err != nil || filepath.HasPrefix(rel, "..") {
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

	stackID := fmt.Sprintf("%s:default", req.Name)
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

	if err := h.scanner.ScanDirectory(stackDir); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"SCANNER_ERROR",
			"Failed to scan new stack directory",
		))
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), stackID, "create", fmt.Sprintf("Created new stack: %s", req.Name))

	deployed := false
	var deployOutput string

	if req.Deploy && h.docker != nil {
		result, err := h.docker.Start(stack)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.NewAppErrorWithDetails(
				http.StatusInternalServerError,
				models.ErrDockerOperation,
				"Stack created but deployment failed",
				gin.H{
					"stack":       stack,
					"lintResults": lintResults,
					"deployed":    false,
					"deployError": err.Error(),
				},
			))
			return
		}
		deployed = true
		deployOutput = result.Stdout

		status, containers, err := h.docker.Status(stack)
		if err == nil {
			stack.Status = status
			stack.Containers = containers
		}

		h.db.UpdateStackStatus(stackID, stack.Status)
		h.logAction(userID.(string), stackID, "start", deployOutput)
	}

	c.JSON(http.StatusCreated, gin.H{
		"stack":        stack,
		"lintResults":  lintResults,
		"deployed":     deployed,
		"deployOutput": deployOutput,
	})
}

func (h *StacksHandler) List(c *gin.Context) {
	stacks, err := h.db.ListStacks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to list stacks",
		))
		return
	}

	for i := range stacks {
		if h.docker != nil {
			status, containers, err := h.docker.Status(stacks[i])
			if err == nil {
				stacks[i].Status = status
				stacks[i].Containers = containers
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"stacks": stacks,
	})
}

func (h *StacksHandler) Get(c *gin.Context) {
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

	if h.docker != nil {
		status, containers, err := h.docker.Status(*stack)
		if err == nil {
			stack.Status = status
			stack.Containers = containers
		}
	}

	c.JSON(http.StatusOK, stack)
}

func (h *StacksHandler) Start(c *gin.Context) {
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

	startTime := time.Now()
	result, err := h.docker.Start(*stack)
	duration := time.Since(startTime)

	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "start", result.Stdout)

	h.db.UpdateStackStatus(id, "running")

	c.JSON(http.StatusOK, gin.H{
		"status":   "started",
		"output":   result.Stdout,
		"duration": duration.Milliseconds(),
	})
}

func (h *StacksHandler) Stop(c *gin.Context) {
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

	startTime := time.Now()
	result, err := h.docker.Stop(*stack)
	duration := time.Since(startTime)

	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "stop", result.Stdout)

	h.db.UpdateStackStatus(id, "stopped")

	c.JSON(http.StatusOK, gin.H{
		"status":   "stopped",
		"output":   result.Stdout,
		"duration": duration.Milliseconds(),
	})
}

func (h *StacksHandler) Restart(c *gin.Context) {
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

	startTime := time.Now()
	result, err := h.docker.Restart(*stack)
	duration := time.Since(startTime)

	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "restart", result.Stdout)

	c.JSON(http.StatusOK, gin.H{
		"status":   "restarted",
		"output":   result.Stdout,
		"duration": duration.Milliseconds(),
	})
}

func (h *StacksHandler) Pull(c *gin.Context) {
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

	startTime := time.Now()
	result, err := h.docker.Pull(*stack)
	duration := time.Since(startTime)

	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "pull", result.Stdout)

	restartAfterPull := c.Query("restart") == "true"

	if restartAfterPull {
		_, err = h.docker.Restart(*stack)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "pulled",
		"output":    result.Stdout,
		"restarted": restartAfterPull,
		"duration":  duration.Milliseconds(),
	})
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

	startTime := time.Now()
	result, err := h.docker.Delete(*stack)
	duration := time.Since(startTime)

	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "delete", result.Stdout)

	h.db.DeleteStack(id)

	c.JSON(http.StatusOK, gin.H{
		"status":   "deleted",
		"output":   result.Stdout,
		"duration": duration.Milliseconds(),
	})
}

func (h *StacksHandler) logAction(userID, stackID, action, detail string) {
	h.actionLog.Log(userID, &stackID, action, detail)
}
