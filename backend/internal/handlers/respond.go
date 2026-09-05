package handlers

import (
	"errors"
	"log/slog"
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
//
// Every 5xx also emits one ERROR line carrying the error chain (agent-os-7z8c).
// The response body deliberately withholds the cause from the client, and the
// fallback branch below does not even read err — it mints a fresh generic
// AppError — so before this, a 500 left no record anywhere of WHY. OBSERVED in
// production: three /api/v1/git/log 500s across 72h of logs produced zero
// explanatory lines, and diagnosing them took ssh, docker inspect and a source
// read that one log line would have replaced.
func handleError(c *gin.Context, err error) {
	var appErr *models.AppError
	if errors.As(err, &appErr) {
		logServerFault(c, appErr.Status, appErr.Code, err)
		c.JSON(appErr.Status, appErr)
		return
	}

	logServerFault(c, http.StatusInternalServerError, "INTERNAL_ERROR", err)
	c.JSON(http.StatusInternalServerError, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error"))
}

// logServerFault emits one ERROR line for a 5xx response, carrying the error
// chain that the response body withholds from the client.
//
// It logs the cause and nothing else that is already recorded elsewhere:
// middleware.LoggingMiddleware logs method, path, status, duration and
// request_id at ERROR for every 5xx already, so duplicating those here would
// add volume without information. Join the two lines on request_id.
//
// Silent below 500 on purpose. handleError's callers map validation, auth and
// not-found conditions through it too; those are the client's fault rather
// than a server fault, LoggingMiddleware already records them at WARN, and
// logging all of them at ERROR would bury the 5xx lines this exists to surface.
//
// Safe under gin.CreateTestContext, where c.Request is nil: the only thing
// touched on c is its key/value store, via middleware.RequestIDFrom, which
// nil-checks c and never reads c.Request. That is also why this is slog.Error
// and not slog.ErrorContext — the context lives on c.Request, so ErrorContext
// would reintroduce exactly the nil dereference this avoids.
//
// Called BEFORE c.JSON deliberately, and nothing in the tests pins that
// (a mutation moving it after c.JSON passes). Keep it first anyway: c.JSON is
// the call that can fail to deliver — it is a no-op on a hijacked WebSocket
// writer, and it panics on a malformed status — so logging first is the
// ordering that still produces a diagnostic in precisely the cases where
// the response does not.
func logServerFault(c *gin.Context, status int, code string, err error) {
	if status < http.StatusInternalServerError {
		return
	}

	attrs := []any{
		"request_id", middleware.RequestIDFrom(c),
		"status", status,
		"code", code,
		"error", err,
	}

	// An AppError's Error() returns only its sanitised, client-facing
	// Message (models/errors.go), so when one carries a Cause (set by
	// respondDockerErr / respondIfEncryptionUnavailable below), surface it
	// here too — otherwise the log line is no more informative than the
	// response body it is meant to supplement. "error", err above is left
	// exactly as-is so the existing agent-os-7z8c assertions on it are
	// unaffected; this only ever adds an attr, never replaces one.
	var appErr *models.AppError
	if errors.As(err, &appErr) && appErr.Cause != nil {
		attrs = append(attrs, "cause", appErr.Cause)
	}

	slog.Error("request failed", attrs...)
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
		handleError(c, models.NewAppErrorWithCause(http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", DockerUnavailableMessage, err))
		return
	}
	handleError(c, models.NewAppErrorWithCause(status, code, message, err))
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
	handleError(c, models.NewAppErrorWithCause(http.StatusUnprocessableEntity, models.ErrEncryptionUnavailable, EncryptionUnavailableMessage, err))
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
