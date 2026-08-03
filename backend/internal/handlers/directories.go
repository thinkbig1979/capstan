package handlers

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type DirectoriesHandler struct {
	scanner *services.ScannerService
	db      *database.DB
}

func NewDirectoriesHandler(scanner *services.ScannerService, db *database.DB) *DirectoriesHandler {
	return &DirectoriesHandler{
		scanner: scanner,
		db:      db,
	}
}

func (h *DirectoriesHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("", h.List)
	group.POST("/scan", h.Scan)
	group.GET("/:path", h.Get)
	group.PUT("/credentials", h.UpdateCredentials)
}

func (h *DirectoriesHandler) List(c *gin.Context) {
	directories, err := h.db.ListDirectories()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to list directories",
		))
		return
	}

	type DirWithCount struct {
		models.Directory
		StackCount int `json:"stackCount"`
	}

	result := make([]DirWithCount, 0, len(directories))
	for _, dir := range directories {
		stacks, _ := h.db.ListStacksByDirectory(dir.Path)
		result = append(result, DirWithCount{
			Directory:  dir,
			StackCount: len(stacks),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"directories": result,
	})
}

func (h *DirectoriesHandler) Scan(c *gin.Context) {
	hasGlobalEnv, err := h.scanner.ScanAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to scan directories",
		))
		return
	}

	redactedDirs, _ := h.db.ListDirectories()
	directoriesCount := len(redactedDirs)
	stacks, _ := h.db.ListStacks()
	stacksCount := len(stacks)

	slog.Info("Directory scan completed", "directories", directoriesCount, "stacks", stacksCount)

	c.JSON(http.StatusOK, gin.H{
		"directories":  redactedDirs,
		"hasGlobalEnv": hasGlobalEnv,
		"scannedAt":    time.Now(),
	})
}

func (h *DirectoriesHandler) Get(c *gin.Context) {
	path := c.Param("path")

	directory, err := h.db.GetDirectory(path)
	if err != nil || directory == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"Directory not found",
		))
		return
	}

	stacks, err := h.db.ListStacksByDirectory(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to list stacks",
		))
		return
	}

	type DirWithStacks struct {
		models.Directory
		Stacks []models.Stack `json:"stacks"`
	}

	c.JSON(http.StatusOK, DirWithStacks{
		Directory: *directory,
		Stacks:    stacks,
	})
}

func (h *DirectoriesHandler) UpdateCredentials(c *gin.Context) {
	var req struct {
		Path       string `json:"path" binding:"required"`
		AuthType   string `json:"authType"`
		SSHKeyPath string `json:"sshKeyPath"`
		HTTPSUser  string `json:"httpsUser"`
		HTTPSToken string `json:"httpsToken"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body: path is required",
		))
		return
	}

	directory, err := h.db.GetDirectory(req.Path)
	if err != nil || directory == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"Directory not found",
		))
		return
	}

	authType := strings.ToLower(req.AuthType)
	if authType != "" && authType != "ssh" && authType != "https" && authType != "inherit" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"authType must be 'ssh', 'https', 'inherit', or empty",
		))
		return
	}

	if err := h.db.UpdateDirectoryCredentials(directory.Path, authType, req.SSHKeyPath, req.HTTPSUser, req.HTTPSToken); err != nil {
		slog.Error("Failed to update directory credentials", "path", directory.Path, "error", err)
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to update credentials",
		))
		return
	}

	updated, err := h.db.GetDirectory(directory.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Failed to retrieve updated directory",
		))
		return
	}

	slog.Info("Directory credentials updated", "path", directory.Path, "authType", authType)

	updated.GitHTTPSToken = ""

	c.JSON(http.StatusOK, gin.H{
		"directory": updated,
	})
}
