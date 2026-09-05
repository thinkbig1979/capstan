package handlers

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
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

// EnvResponse is the wire shape of GET /:id/env.
//
// Raw is omitempty and Locked is set because the response is redacted for a
// session that has not re-entered its password: see redactEnvResponse. A caller
// must therefore treat a missing "raw" as "not authorised to see it", not as
// "the file is empty" — Locked tells the two apart.
type EnvResponse struct {
	Filename string     `json:"filename"`
	Entries  []EnvEntry `json:"entries"`
	Raw      string     `json:"raw,omitempty"`
	Locked   bool       `json:"locked,omitempty"`
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
	group.POST("/:id/env", h.Create)
}

func (h *EnvHandler) Get(c *gin.Context) {
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

	//nolint:gosec // envPath was validated against the configured stacks directories above (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
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
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "READ_ERROR", "Failed to read env file", err))
		return
	}

	entries := h.parseEnvFile(string(content))

	resp := EnvResponse{
		Filename: stack.EnvFile,
		Entries:  entries,
		Raw:      string(content),
		Locked:   !envUnlocked(c),
	}
	if resp.Locked {
		redactEnvResponse(&resp)
	}

	c.JSON(http.StatusOK, resp)
}

// envUnlocked reports whether this request carried a valid env-unlock token, as
// determined upstream by middleware.EnvUnlock. Reading the context (rather than
// the header) keeps token validation in one place and makes a route that was
// never wired through the middleware read false — i.e. fail closed.
func envUnlocked(c *gin.Context) bool {
	return c.GetBool(middleware.CtxEnvUnlocked)
}

// redactEnvResponse strips the secrets from an env payload for a session that
// has not re-entered its password.
//
// It blanks sensitive entry values as well as dropping Raw, because Raw is not
// the only copy: parseEnvFile puts the same plaintext into every
// entries[].Value, and Sensitive is only a client-side masking hint. Dropping
// Raw alone would leave a caller refused the raw file free to read
// entries[].value instead.
//
// Keys, line numbers, comments and non-sensitive values survive, so a locked
// session can still see the shape of the file (which variables a stack defines,
// what its TZ is) without seeing any secret.
func redactEnvResponse(resp *EnvResponse) {
	resp.Raw = ""
	for i := range resp.Entries {
		if resp.Entries[i].Sensitive {
			resp.Entries[i].Value = ""
		}
	}
}

func (h *EnvHandler) Put(c *gin.Context) {
	id := c.Param("id")

	// The write path is gated as well as the read path, and not for symmetry: a
	// locked session was handed blanked sensitive values by Get, so letting it
	// PUT what it holds would overwrite every secret in the file with "". The
	// gate is what makes the redaction safe rather than destructive.
	if !envUnlocked(c) {
		c.JSON(http.StatusForbidden, models.NewAppError(
			http.StatusForbidden,
			models.ErrForbidden,
			"Re-enter your password to edit environment variables",
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
		// Validate before serialising: reject entries with a non-comment, non-blank
		// key that is empty — they would produce a corrupt "=value" line (#15).
		if err := validateEnvEntries(req.Entries); err != nil {
			renderResult(c, truth.Failed("env validation failed: "+err.Error(), err))
			return
		}
		content = serializeEnvFile(req.Entries)
	} else {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"Must provide either 'entries' or 'raw'",
		))
		return
	}

	if err := writeEnvFileAtomic(envPath, content); err != nil {
		renderResult(c, truth.Failed("failed to write env file", err))
		return
	}

	// Round-trip verify: re-read the file and confirm the bytes were persisted
	// faithfully (#15 file-save contract).
	if ar := verifyEnvRoundTrip(envPath, content); ar != nil {
		renderResult(c, *ar)
		return
	}

	h.logAction(c, id, "update_env", "Updated env file: "+stack.EnvFile)

	renderResult(c, truth.Success("env file saved",
		truth.KV("filename", stack.EnvFile),
	))
}

// Create handles POST /api/v1/stacks/:id/env — creates the stack's .env file.
// Returns no_change (409) if the file already exists, success once the file is
// verified to exist on disk (#16).
func (h *EnvHandler) Create(c *gin.Context) {
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

	// Determine the env file path: use the configured one when set, otherwise
	// default to ".env" in the stack directory.
	envFileName := stack.EnvFile
	if envFileName == "" {
		envFileName = ".env"
	}
	envPath := filepath.Join(stack.Directory, envFileName)

	if err := validateStackPath(envPath, h.config); err != nil {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrPathTraversal,
			"Invalid env file path",
		))
		return
	}

	// Refuse if the file already exists.
	if _, statErr := os.Stat(envPath); statErr == nil {
		c.JSON(http.StatusConflict, truth.NoChange("env file already exists",
			truth.KV("filename", envFileName),
		))
		return
	}

	// Accept optional initial content from the request body.
	var req struct {
		Content string `json:"content"`
		Raw     string `json:"raw"`
	}
	// Ignore bind error — an empty body is fine (creates an empty file).
	_ = c.ShouldBindJSON(&req)

	content := req.Content
	if content == "" {
		content = req.Raw
	}

	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		renderResult(c, truth.Failed("failed to create env file", err))
		return
	}

	// Verify the file was actually created on disk.
	if _, statErr := os.Stat(envPath); statErr != nil {
		renderResult(c, truth.Failed("env file was not created on disk", statErr,
			truth.KV("filename", envFileName),
		))
		return
	}

	// Update the stack record if it had no env file configured.
	if stack.EnvFile == "" {
		stack.EnvFile = envFileName
		if err := h.db.UpsertStack(*stack); err != nil {
			// Non-fatal: file exists, DB update failed. Surface as partial.
			renderResult(c, truth.Partial("env file created but DB not updated",
				truth.KV("filename", envFileName),
				truth.KV("dbError", err.Error()),
			))
			return
		}
	}

	h.logAction(c, id, "create_env", "Created env file: "+envFileName)

	c.JSON(http.StatusCreated, truth.Success("env file created",
		truth.KV("filename", envFileName),
	))
}

// validateEnvEntries returns an error when any non-comment, non-blank entry
// has an empty key. Such entries would produce a corrupt "=value" line when
// serialised (finding #15).
func validateEnvEntries(entries []EnvEntry) error {
	for i, e := range entries {
		if e.Comment {
			continue
		}
		// A blank entry (key=="", value=="") is a blank line — allowed.
		if e.Key == "" && e.Value == "" {
			continue
		}
		// A non-blank value with an empty key would produce "=value".
		if e.Key == "" && e.Value != "" {
			return fmt.Errorf("entry at index %d has an empty key and non-empty value %q; this would produce a corrupt env line", i, e.Value)
		}
		// A literal newline (or carriage return) in a key or value would split
		// the entry across multiple lines on serialisation, silently corrupting
		// the file. The round-trip check can't distinguish this from intent, so
		// reject it up front (finding B4).
		if strings.ContainsAny(e.Key, "\r\n") {
			return fmt.Errorf("entry at index %d has a key containing a newline; this would corrupt the env file", i)
		}
		if strings.ContainsAny(e.Value, "\r\n") {
			return fmt.Errorf("entry at index %d (key %q) has a value containing a newline; this would corrupt the env file", i, e.Key)
		}
	}
	return nil
}

// writeEnvFileAtomic writes content to envPath via a temp-file-plus-rename so
// the update is atomic and never exposes the file at a looser mode than 0600.
// Writing in place (os.WriteFile then os.Chmod) leaves a window, between the
// truncating write and the chmod, where a pre-existing looser-mode file (e.g.
// 0644 left by older code) holds the freshly written secrets at that looser
// mode. The temp file is created directly at 0600 in envPath's own directory,
// so os.Rename lands the final file at 0600 in one atomic step and stays on
// a single filesystem.
func writeEnvFileAtomic(envPath, content string) error {
	dir := filepath.Dir(envPath)
	base := filepath.Base(envPath)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	// os.CreateTemp already opens the file at 0600, but set the mode
	// explicitly on the fd rather than relying on that documented default.
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write([]byte(content)); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, envPath); err != nil {
		return fmt.Errorf("rename temp file into place: %w", err)
	}
	return nil
}

// verifyEnvRoundTrip re-reads envPath and confirms the persisted bytes equal
// the intended content. Returns a Failed ActionResult on mismatch, nil on success.
func verifyEnvRoundTrip(envPath, intended string) *truth.ActionResult {
	//nolint:gosec // callers validate envPath against the configured stacks directories before calling this (validateStackPath, symlink-aware) — see README.md "Command execution and file access"
	persisted, err := os.ReadFile(envPath)
	if err != nil {
		ar := truth.Failed("could not re-read env file for round-trip verification", err)
		return &ar
	}
	if string(persisted) != intended {
		ar := truth.Failed("env file round-trip verification failed: persisted bytes differ from intended content",
			nil,
			truth.KV("expectedLen", len(intended)),
			truth.KV("gotLen", len(persisted)),
		)
		return &ar
	}
	return nil
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

func serializeEnvFile(entries []EnvEntry) string {
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
	return isSensitiveEnvKey(key)
}

// isSensitiveEnvKey classifies a key as secret-bearing by name. Shared with the
// global-env surface in settings.go so both redact on the same rule — a key that
// masks in one place and leaks in the other is the whole bug class.
func isSensitiveEnvKey(key string) bool {
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

	return strings.Contains(upperKey, "_API_")
}

func (h *EnvHandler) logAction(c *gin.Context, stackID, action, detail string) {
	h.actionLog.LogWithRequest(middleware.RequestIDFrom(c), userIDFrom(c), &stackID, action, detail)
}
