package handlers

import (
	"database/sql"
	"errors"
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
	group.GET("/credential-status", h.CredentialStatus)
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
		if respondIfEncryptionUnavailable(c, err) {
			return
		}
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

// Credential status values returned by CredentialStatus. These are a fixed
// enum, never err.Error() or any part of the stored credential — see the
// doc comment on CredentialStatus for why.
const (
	credentialStatusNone       = "none"
	credentialStatusOK         = "ok"
	credentialStatusUnreadable = "unreadable"
	credentialStatusEmpty      = "empty"
)

// CredentialStatus reports whether path's stored git HTTPS credential is
// usable, without exposing the token or its ciphertext anywhere (response,
// log, or action log).
//
// It exists because ListDirectories/GetDirectory deliberately never decrypt
// git_https_token (see the comment on ListDirectories in
// database/directories.go) and so cannot tell an operator that a directory's
// saved credential no longer decrypts under the current STORAGE_KEY
// (agent-os-2au): today that failure is visible only as a slog.Error line
// server-side. This is a separate, per-directory probe rather than a change
// to ListDirectories, so the "never decrypts" invariant and its scan cost
// stay exactly as they are.
//
// The path travels as a query parameter, not a URL segment: main.go builds a
// bare gin.New() with no UseRawPath, so gin matches on the already-decoded
// URL.Path, and an encoded "/" in a wildcard segment does not round-trip
// (agent-os-p7r, PR #89).
//
// Four states are reported:
//   - "none": no HTTPS credential is configured for this directory (no
//     stored authType, or authType "ssh"/"inherit" — this probe only
//     concerns the HTTPS token, which is the only encrypted credential
//     material GetDirectoryCredentials decrypts).
//   - "ok": an HTTPS credential is configured and decrypted successfully.
//   - "unreadable": a credential is stored but GetDirectoryCredentials could
//     not decrypt it — a rotated STORAGE_KEY, or a legacy plaintext value
//     (crypto.go's two distinct decrypt-failure constants). The specific
//     reason is deliberately not returned; only the state is.
//   - "empty": authType is "https" but no token was ever saved. This is the
//     state documented in git_credentials.go's httpsCredentials: otherwise
//     indistinguishable from "everything is fine" until a git operation
//     fails with a generic auth error, and the UI reports the directory as
//     configured even though nothing was saved. Collapsing this into "none"
//     would leave that exact symptom unfixed, so it gets its own state.
func (h *DirectoriesHandler) CredentialStatus(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, models.NewAppError(
			http.StatusBadRequest,
			models.ErrValidation,
			"path query parameter is required",
		))
		return
	}

	directory, err := h.db.GetDirectory(path)
	if err != nil || directory == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrNotFound,
			"Directory not found",
		))
		return
	}

	cred, err := h.db.GetDirectoryCredentials(path)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// NOT the ordinary "never configured" case — that is handled by the
		// authType=="" fall-through below, since GetDirectory just above
		// already confirmed a row exists at this path. GetDirectory and
		// GetDirectoryCredentials query the same table by the same key
		// (database/directories.go), so the only way THIS call can still see
		// sql.ErrNoRows is a TOCTOU race: the row was deleted between the two
		// queries. Kept as defence rather than deleted, but deliberately
		// untested — a test would have to win a race against this handler's
		// own two DB calls, which costs more than the branch is worth.
		c.JSON(http.StatusOK, gin.H{"path": path, "status": credentialStatusNone})
		return
	case err != nil:
		// A decrypt failure. Never log cred or err's message here: err can
		// wrap crypto output, and logging it risks writing ciphertext or
		// derived key material to disk for no operator benefit — the state
		// alone ("unreadable") is the actionable signal.
		slog.Error("directory git credential cannot be decrypted", "path", path)
		c.JSON(http.StatusOK, gin.H{"path": path, "status": credentialStatusUnreadable})
		return
	}

	status := credentialStatusNone
	if strings.ToLower(cred.GitAuthType) == "https" {
		if cred.GitHTTPSToken == "" {
			status = credentialStatusEmpty
		} else {
			status = credentialStatusOK
		}
	}

	c.JSON(http.StatusOK, gin.H{"path": path, "status": status})
}
