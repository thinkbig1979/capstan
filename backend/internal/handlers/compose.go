package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
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
