package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type GitHandler struct {
	git       *services.GitService
	docker    *services.DockerService
	db        *database.DB
	config    *config.Config
	actionLog *services.ActionLogger
}

func NewGitHandler(git *services.GitService, docker *services.DockerService, db *database.DB, cfg *config.Config) *GitHandler {
	return &GitHandler{
		git:       git,
		docker:    docker,
		db:        db,
		config:    cfg,
		actionLog: services.NewActionLogger(db),
	}
}

func (h *GitHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("", h.GetStatus)
	group.POST("/pull", h.Pull)
	group.GET("/log", h.GetLog)
	group.GET("/diff/:hash", h.GetDiff)
}

func (h *GitHandler) resolvePathFromStack(c *gin.Context) (string, string, error) {
	stackID := c.Query("stackId")
	if stackID != "" {
		stack, err := h.db.GetStack(stackID)
		if err != nil {
			return "", "", models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Stack not found")
		}
		normalizedDir, err := filepath.Abs(stack.Directory)
		if err != nil {
			return "", "", models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve stack directory")
		}
		return normalizedDir, stackID, nil
	}

	dirParam := c.Query("dir")
	if dirParam == "" {
		return "", "", models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Missing stackId or dir query parameter")
	}

	decodedPath, err := url.PathUnescape(dirParam)
	if err != nil {
		return "", "", models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid path encoding")
	}

	var absPath string
	if filepath.IsAbs(decodedPath) {
		absPath = decodedPath
	} else {
		for _, stacksDir := range h.config.GetAllStacksDirs() {
			candidate := filepath.Join(stacksDir, decodedPath)
			if _, err := os.Stat(candidate); err == nil {
				absPath = candidate
				break
			}
		}
		if absPath == "" {
			absPath = filepath.Join(h.config.StacksDir, decodedPath)
		}
	}

	normalizedAbs, err := filepath.Abs(absPath)
	if err != nil {
		return "", "", models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid path")
	}

	valid := false
	for _, stacksDir := range h.config.GetAllStacksDirs() {
		normalizedStacks, err := filepath.Abs(stacksDir)
		if err != nil {
			continue
		}
		if strings.HasPrefix(normalizedAbs, normalizedStacks) {
			valid = true
			break
		}
	}

	if !valid {
		return "", "", models.NewAppError(http.StatusForbidden, models.ErrPathTraversal, "Path traversal not allowed")
	}

	return normalizedAbs, "", nil
}

func (h *GitHandler) GetStatus(c *gin.Context) {
	absPath, _, err := h.resolvePathFromStack(c)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	status, err := h.git.GetStatus(absPath)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"branch":        status.Branch,
		"commit":        status.Commit.Hash,
		"commitShort":   status.Commit.Short,
		"commitMessage": status.Commit.Message,
		"commitAuthor":  status.Commit.Author,
		"commitDate":    status.Commit.Date,
		"dirty":         status.Dirty,
		"dirtyCount":    status.DirtyCount,
		"ahead":         status.Ahead,
		"behind":        status.Behind,
		"remote":        status.RemoteURL,
	})
}

func (h *GitHandler) Pull(c *gin.Context) {
	absPath, _, err := h.resolvePathFromStack(c)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	result, err := h.git.Pull(absPath)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	redeploy := c.Query("redeploy") == "true"
	var redeployedStacks []string

	if redeploy && h.docker != nil {
		redeployedStacks = h.redeployAffectedStacks(absPath, result.ChangedFiles)
	}

	userID, _ := c.Get("userID")
	detail := h.formatPullDetail(result, redeployedStacks)
	h.logGitAction(userID.(string), absPath, "pull", detail)

	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"previousCommit":   result.PreviousCommit,
		"currentCommit":    result.CurrentCommit,
		"changedFiles":     result.ChangedFiles,
		"redeployedStacks": redeployedStacks,
	})
}

func (h *GitHandler) redeployAffectedStacks(dirPath string, changedFiles []string) []string {
	if len(changedFiles) == 0 {
		return []string{}
	}

	stacks, err := h.db.ListStacksByDirectory(dirPath)
	if err != nil {
		slog.Warn("Failed to list stacks for redeploy", "path", dirPath, "error", err)
		return []string{}
	}

	redeployed := []string{}

	for _, stack := range stacks {
		if h.stackFilesChanged(stack, changedFiles) {
			slog.Info("Redeploying stack", "stackId", stack.ID, "changedFiles", changedFiles)

			result, err := h.docker.Restart(stack)
			if err != nil {
				slog.Warn("Failed to redeploy stack", "stackId", stack.ID, "error", err)
				continue
			}

			slog.Info("Stack redeployed", "stackId", stack.ID, "output", result.Stdout)
			redeployed = append(redeployed, stack.ID)
		}
	}

	return redeployed
}

func (h *GitHandler) stackFilesChanged(stack models.Stack, changedFiles []string) bool {
	for _, file := range changedFiles {
		if file == stack.ComposeFile || file == stack.EnvFile {
			return true
		}
	}
	return false
}

func (h *GitHandler) formatPullDetail(result *models.PullResult, redeployedStacks []string) string {
	detail := ""
	if result.PreviousCommit != result.CurrentCommit {
		prevShort := result.PreviousCommit
		if len(prevShort) > 7 {
			prevShort = prevShort[:7]
		}
		currShort := result.CurrentCommit
		if len(currShort) > 7 {
			currShort = currShort[:7]
		}
		detail = "Pulled " + prevShort + " -> " + currShort
	} else {
		detail = "Already up to date"
	}

	if len(redeployedStacks) > 0 {
		detail += ", redeployed: " + strings.Join(redeployedStacks, ", ")
	}

	return detail
}

func (h *GitHandler) GetLog(c *gin.Context) {
	absPath, _, err := h.resolvePathFromStack(c)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := parseQueryParamInt(l, 50, 200); err == nil {
			limit = parsed
		}
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if parsed, err := parseQueryParamInt(o, 0, 10000); err == nil {
			offset = parsed
		}
	}

	file := c.Query("file")

	var result *models.LogResult

	if file != "" {
		result, err = h.git.GetLogForFile(absPath, file, limit)
	} else {
		result, err = h.git.GetLog(absPath, limit, offset)
	}

	if err != nil {
		models.HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"commits": result.Commits,
		"total":   result.Total,
		"hasMore": result.HasMore,
	})
}

func (h *GitHandler) GetDiff(c *gin.Context) {
	absPath, _, err := h.resolvePathFromStack(c)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	hash := c.Param("hash")

	if !isValidHash(hash) {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid commit hash format",
		))
		return
	}

	result, err := h.git.GetDiff(absPath, hash)
	if err != nil {
		models.HandleError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"commit": result.Commit,
		"diff":   result.Diff,
	})
}

func (h *GitHandler) logGitAction(userID, absPath, action, detail string) {
	h.actionLog.Log(userID, nil, action, detail)
}

func parseQueryParamInt(value string, min, max int) (int, error) {
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return min, err
	}
	if parsed < min {
		return min, nil
	}
	if parsed > max {
		return max, nil
	}
	return parsed, nil
}

func isValidHash(hash string) bool {
	if len(hash) == 40 {
		return regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(hash)
	}
	if len(hash) >= 7 {
		return regexp.MustCompile(`^[0-9a-f]{7,}$`).MatchString(hash)
	}
	return false
}
