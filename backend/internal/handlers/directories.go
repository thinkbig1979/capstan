package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
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

	directories, _ := h.db.ListDirectories()
	directoriesCount := len(directories)
	stacks, _ := h.db.ListStacks()
	stacksCount := len(stacks)

	slog.Info("Directory scan completed", "directories", directoriesCount, "stacks", stacksCount)

	c.JSON(http.StatusOK, gin.H{
		"directories":  directories,
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
