package handlers

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type EnvHandler struct {
	db        *database.DB
	config    *config.Config
	actionLog *services.ActionLogger
}

type EnvEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Line      int    `json:"line"`
	Sensitive bool   `json:"sensitive,omitempty"`
	Comment   bool   `json:"comment,omitempty"`
}

type EnvResponse struct {
	Filename string     `json:"filename"`
	Entries  []EnvEntry `json:"entries"`
	Raw      string     `json:"raw"`
}

type EnvRequest struct {
	Entries []EnvEntry `json:"entries"`
	Raw     string     `json:"raw"`
}

type EnvSaveResponse struct {
	Saved    bool   `json:"saved"`
	Filename string `json:"filename"`
}

func NewEnvHandler(db *database.DB, config *config.Config) *EnvHandler {
	return &EnvHandler{
		db:        db,
		config:    config,
		actionLog: services.NewActionLogger(db),
	}
}

func (h *EnvHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/:id/env", h.Get)
	group.PUT("/:id/env", h.Put)
}

func (h *EnvHandler) Get(c *gin.Context) {
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

	if stack.EnvFile == "" {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"No env file associated with this stack",
		))
		return
	}

	envPath := filepath.Join(stack.Directory, stack.EnvFile)

	if err := validateStackPath(envPath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid env file path",
		))
		return
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, models.NewAppError(
				http.StatusNotFound,
				models.ErrNotFound,
				"Env file not found on disk",
			))
			return
		}
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"READ_ERROR",
			"Failed to read env file",
		))
		return
	}

	entries := h.parseEnvFile(string(content))

	c.JSON(http.StatusOK, EnvResponse{
		Filename: stack.EnvFile,
		Entries:  entries,
		Raw:      string(content),
	})
}

func (h *EnvHandler) Put(c *gin.Context) {
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

	if stack.EnvFile == "" {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"No env file associated with this stack",
		))
		return
	}

	envPath := filepath.Join(stack.Directory, stack.EnvFile)

	if err := validateStackPath(envPath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid env file path",
		))
		return
	}

	var req EnvRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Invalid request body",
		))
		return
	}

	var content string

	if req.Raw != "" {
		content = req.Raw
	} else if len(req.Entries) > 0 {
		content = h.serializeEnvFile(req.Entries)
	} else {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Must provide either 'entries' or 'raw'",
		))
		return
	}

	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewAppError(
			http.StatusInternalServerError,
			"WRITE_ERROR",
			"Failed to write env file",
		))
		return
	}

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "update_env", "Updated env file: "+stack.EnvFile)

	c.JSON(http.StatusOK, EnvSaveResponse{
		Saved:    true,
		Filename: stack.EnvFile,
	})
}

func (h *EnvHandler) parseEnvFile(content string) []EnvEntry {
	var entries []EnvEntry
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineNum++

		trimmedLine := strings.TrimSpace(line)

		if trimmedLine == "" {
			entries = append(entries, EnvEntry{
				Key:     "",
				Value:   "",
				Line:    lineNum,
				Comment: false,
			})
			continue
		}

		if strings.HasPrefix(trimmedLine, "#") {
			entries = append(entries, EnvEntry{
				Key:     "",
				Value:   trimmedLine,
				Line:    lineNum,
				Comment: true,
			})
			continue
		}

		eqIndex := strings.Index(trimmedLine, "=")
		if eqIndex == -1 {
			entries = append(entries, EnvEntry{
				Key:     "",
				Value:   trimmedLine,
				Line:    lineNum,
				Comment: true,
			})
			continue
		}

		key := strings.TrimSpace(trimmedLine[:eqIndex])
		value := strings.TrimSpace(trimmedLine[eqIndex+1:])

		value = h.unquoteValue(value)

		if inlineCommentIndex := strings.Index(value, " #"); inlineCommentIndex != -1 {
			value = strings.TrimSpace(value[:inlineCommentIndex])
		}

		entries = append(entries, EnvEntry{
			Key:       key,
			Value:     value,
			Line:      lineNum,
			Sensitive: h.isSensitiveKey(key),
			Comment:   false,
		})
	}

	return entries
}

func (h *EnvHandler) unquoteValue(value string) string {
	value = strings.TrimSpace(value)

	if len(value) >= 2 {
		if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			return value[1 : len(value)-1]
		}
		if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			return value[1 : len(value)-1]
		}
	}

	return value
}

func (h *EnvHandler) serializeEnvFile(entries []EnvEntry) string {
	var buf bytes.Buffer

	for _, entry := range entries {
		if entry.Comment {
			if entry.Key == "" && entry.Value == "" {
				buf.WriteString("\n")
			} else {
				buf.WriteString(entry.Value)
				buf.WriteString("\n")
			}
		} else if entry.Key == "" && entry.Value == "" {
			buf.WriteString("\n")
		} else {
			buf.WriteString(entry.Key)
			buf.WriteString("=")
			buf.WriteString(entry.Value)
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

func (h *EnvHandler) isSensitiveKey(key string) bool {
	upperKey := strings.ToUpper(key)

	if strings.HasPrefix(upperKey, "EXPORT ") {
		upperKey = strings.TrimSpace(upperKey[7:])
	}

	sensitiveSuffixes := []string{"_KEY", "_SECRET", "_PASSWORD", "_TOKEN"}
	for _, suffix := range sensitiveSuffixes {
		if strings.HasSuffix(upperKey, suffix) {
			return true
		}
	}

	if strings.Contains(upperKey, "_API_") {
		return true
	}

	return false
}

func (h *EnvHandler) logAction(userID, stackID, action, detail string) {
	h.actionLog.Log(userID, &stackID, action, detail)
}
