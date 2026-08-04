package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// renderResult writes the ActionResult as JSON to a gin context using the
// appropriate HTTP status code. All action endpoints should use this
// to ensure consistent wire format across domains.
func renderResult(c *gin.Context, r truth.ActionResult) {
	c.JSON(r.HTTPStatus(), r)
}

// handleError writes err as a JSON error response, using the AppError's
// status and code when available and falling back to a generic 500.
func handleError(c *gin.Context, err error) {
	var appErr *models.AppError
	if errors.As(err, &appErr) {
		c.JSON(appErr.Status, appErr)
		return
	}

	c.JSON(http.StatusInternalServerError, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error"))
}

// DockerUnavailableMessage is the operator-facing text for a Docker outage. It
// says what is wrong and what to check, rather than surfacing a raw Go error.
const DockerUnavailableMessage = "Docker daemon unreachable: the server started without a usable Docker connection. Check that the Docker socket is mounted and the daemon is running, then restart Capstan."

// renderDockerResult writes r the way renderResult does, except when err is the
// Docker outage sentinel: that becomes a 503 carrying the actionable message,
// so an operator sees "Docker daemon unreachable" rather than a generic action
// failure. Action endpoints (which speak truth.ActionResult) use this where
// plain error endpoints use respondDockerErr.
func renderDockerResult(c *gin.Context, err error, r truth.ActionResult) {
	if errors.Is(err, services.ErrDockerUnavailable) {
		c.JSON(http.StatusServiceUnavailable, truth.Failed(DockerUnavailableMessage, err))
		return
	}
	renderResult(c, r)
}

// respondDockerErr writes err as a JSON error response, mapping the Docker
// outage sentinel to 503 DOCKER_UNAVAILABLE and falling back to the caller's
// status/code/message for every other error.
//
// main leaves dockerService nil when the daemon was unreachable at startup, and
// every DockerService method then returns services.ErrDockerUnavailable rather
// than dereferencing a nil receiver (agent-os-xay). This is where that sentinel
// becomes an actionable HTTP response instead of a generic 500.
func respondDockerErr(c *gin.Context, err error, status int, code, message string) {
	if errors.Is(err, services.ErrDockerUnavailable) {
		handleError(c, models.NewAppError(http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", DockerUnavailableMessage))
		return
	}
	handleError(c, models.NewAppError(status, code, message))
}

// EncryptionUnavailableMessage is the operator-facing text for a missing
// at-rest encryption key (agent-os-16m). Startup logs a WARN and continues —
// AUTH_DISABLED is a deliberately usable no-config mode — so the first
// attempt to store an encryptable secret (restic_password, git_https_token)
// is where the gap becomes visible.
const EncryptionUnavailableMessage = "Cannot store this value: no encryption key is configured. Set STORAGE_KEY (or JWT_SECRET) in the environment and restart Capstan, then try again."

// respondIfEncryptionUnavailable writes a clear 422 ENCRYPTION_KEY_MISSING
// response and returns true when err is (or wraps)
// services.ErrEncryptionUnavailable. Callers must return immediately when
// this returns true. This is the settings-write analogue of
// respondDockerErr/renderDockerResult above.
func respondIfEncryptionUnavailable(c *gin.Context, err error) bool {
	if !errors.Is(err, services.ErrEncryptionUnavailable) {
		return false
	}
	handleError(c, models.NewAppError(http.StatusUnprocessableEntity, models.ErrEncryptionUnavailable, EncryptionUnavailableMessage))
	return true
}

// userIDFrom extracts the authenticated userID from the gin context,
// defaulting to "anonymous" when unset.
func userIDFrom(c *gin.Context) string {
	userID := c.GetString("userID")
	if userID == "" {
		userID = "anonymous"
	}
	return userID
}

// logActionFromContext logs an action using the userID found on the gin
// context, delegating to the given ActionLogger.
func logActionFromContext(l *services.ActionLogger, c *gin.Context, stackID *string, action string, detail interface{}) {
	l.LogWithRequest(middleware.RequestIDFrom(c), userIDFrom(c), stackID, action, detail)
}
