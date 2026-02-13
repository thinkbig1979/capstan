package handlers

import (
	"net/http"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type StacksHandler struct {
	docker  *services.DockerService
	scanner *services.ScannerService
	db      *database.DB
}

func NewStacksHandler(docker *services.DockerService, scanner *services.ScannerService, db *database.DB) *StacksHandler {
	return &StacksHandler{
		docker:  docker,
		scanner: scanner,
		db:      db,
	}
}

func (h *StacksHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("", h.List)
	group.GET("/:id", h.Get)
	group.POST("/:id/start", h.Start)
	group.POST("/:id/stop", h.Stop)
	group.POST("/:id/restart", h.Restart)
	group.POST("/:id/pull", h.Pull)
	group.DELETE("/:id", h.Delete)
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
		status, containers, err := h.docker.Status(stacks[i])
		if err == nil {
			stacks[i].Status = status
			stacks[i].Containers = containers
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

	status, containers, err := h.docker.Status(*stack)
	if err == nil {
		stack.Status = status
		stack.Containers = containers
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
	log := models.ActionLog{
		ID:        uuid.New().String(),
		UserID:    userID,
		StackID:   stackID,
		Action:    action,
		Detail:    detail,
		CreatedAt: time.Now(),
	}

	h.db.LogAction(log)
}
