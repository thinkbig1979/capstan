package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// renderResult writes the ActionResult as JSON to a gin context using the
// appropriate HTTP status code. All action endpoints should use this
// to ensure consistent wire format across domains.
func renderResult(c *gin.Context, r truth.ActionResult) {
	renderResultWithStatus(c, r.HTTPStatus(), r)
}

// renderResultWithStatus is the one place an ActionResult becomes a response,
// and therefore the one place a failed one is logged (agent-os-7lic).
//
// ActionResult.Err is json:"-" by design (truth/outcome.go), so the body never
// carries the cause; before this, a Failed result (500 via HTTPStatus, or the
// 503 substitution in renderDockerResult) left no record of WHY anywhere —
// the exact gap agent-os-7z8c closed for handleError. The log is keyed on the
// rendered STATUS, not on how r was built: truth.Failed constructions, the
// ActionResult literals that copy Outcome and Err off a service result in
// stack_lifecycle.go / stack_crud.go, and results passed through unchanged
// from a service (git.go's Pull) all arrive here with the same shape, and a
// log keyed on the constructor would miss the latter two.
//
// The Reason is the message and Err is the cause. Err may be nil on a Failed
// result (a refusal with a Reason and nothing underneath, e.g. the
// path-outside-root guard in stack_crud.go); logServerFault adds the cause
// attr only when the AppError carries one, so a nil Err degrades to "no cause
// attr" without any gating on Err != nil here. The AppError is built solely
// to reuse logServerFault's line shape and never escapes this function, so
// the Unwrap hazard documented on NewAppErrorWithCause (errors.Is seeing
// through a travelling AppError) cannot apply.
//
// Partial (207) is deliberately not logged: logServerFault's < 500 guard
// excludes it, and a 207 already names its failed subset in Details.
//
// Status, code and body are unchanged by this function: the only effect
// beyond c.JSON is the log line.
func renderResultWithStatus(c *gin.Context, status int, r truth.ActionResult) {
	if status >= http.StatusInternalServerError {
		logServerFault(c, status, "ACTION_FAILED", models.NewAppErrorWithCause(status, "ACTION_FAILED", r.Reason, r.Err))
	}
	c.JSON(status, r)
}

// routineErrorCodes lists the AppError codes whose 4xx responses are ROUTINE
// NEGATIVE ANSWERS — a true, expected fact about a resource — rather than
// client errors. handleError marks these so middleware.LoggingMiddleware logs
// them at Info instead of Warn (agent-os-prfj).
//
// EVERY MEMBER IS LISTED EXPLICITLY, one code at a time. This is the class
// boundary, and it is never inferred from the status or the request path:
// GET /api/v1/git answers 404 for BOTH "this directory is not a git
// repository" (routine — the frontend asks it of every stack, and most hosts
// have no git-backed stack at all) and "stack not found" for an unknown
// stackId (git.go's resolvePathFromStack, models.ErrStackNotFound — a real
// client error). Same endpoint, same status, opposite answers; only the code
// separates them.
//
// The list lives here rather than in middleware so middleware carries no
// opinion about which codes are routine. That is a design preference, not an
// import constraint — middleware already imports models (middleware/auth.go,
// middleware/recovery.go), so either placement compiles.
//
// Adding a code here silences a WARN for every response carrying it. Before
// adding one, confirm the code is not ALSO minted for a genuine client error
// somewhere: models.ErrNotFound would fail that test, which is why the two
// in-class env.go sites (agent-os-hjmf) cannot join this list and must call
// middleware.MarkRoutineOutcome directly instead.
//
// The second thing to confirm is that the code means ONE thing at its mint.
// GIT_NOT_REPO only just earned its place: services/git.go's gitFailure used
// to answer it for every way its probe could fail, so an unmounted stacks
// volume and an image with no git binary both arrived here as "Not a git
// repository". Listing it while that was true would have taken two real
// operator incidents off the log, because a code is only as routine as its
// narrowest mint. gitFailure now splits "the probe ran and found no
// repository" from "the probe could not run" on the error TYPE, and only the
// former keeps this code; the latter is a plain wrapped error that becomes a
// 500 and logs its chain. See services/git.go gitFailure, which carries the
// measurements and one accepted limit (an unreadable .git is still
// indistinguishable from an absent one, because git reports them identically).
//
// GIT_NO_COMMITS is KEPT here although agent-os-4a4a made GET /api/v1/git
// answer that condition 200, which is the only route by which it used to reach
// this map. It is defence in depth and not an oversight: services/git.go still
// MINTS the code, and any future caller of GitService.GetStatus that routes it
// to handleError would otherwise reintroduce a WARN for the same routine state.
// Removing the entry would also delete the two rows in
// prfj_routine_404_log_test.go that carry agent-os-n2df's argument for why this
// condition needed a code of its own rather than sharing ErrNotFound.
var routineErrorCodes = map[string]bool{
	models.ErrGitNotRepo:   true,
	models.ErrGitNoCommits: true,
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
// notFoundWire is the ONE place an absence reported by internal/database turns
// into a wire code and a client-facing message (agent-os-ymyc). Before it, each
// of ~18 handler routes re-derived this from sql.ErrNoRows by hand, which is how
// a database that could not answer kept being reported as a resource that does
// not exist.
//
// Every entry reproduces EXACTLY what its routes emitted before the collapse —
// this change is a refactor of where the decision is made, not of what goes on
// the wire. "stack" keeps STACK_NOT_FOUND because 15 routes minted that code and
// docs/reference/api.md documents it.
//
// Four more routes (updates.go, git.go, monitoring.go x2) used to answer an
// absent stack with models.ErrNotFound, so one fact had two wire codes decided
// by which route a client hit. agent-os-symj converged them here, on
// STACK_NOT_FOUND. NOT_FOUND keeps its other genuine client-error mints
// (directory, env file), which is why it stays out of routineErrorCodes.
var notFoundWire = map[string]struct {
	Code    string
	Message string
}{
	"stack":              {models.ErrStackNotFound, "Stack not found"},
	"directory":          {models.ErrNotFound, "Directory not found"},
	"backup run":         {models.ErrNotFound, "Backup run not found"},
	"backup run item":    {models.ErrNotFound, "Backup run item not found"},
	"backup policy":      {models.ErrNotFound, "Backup policy not found"},
	"auto-update policy": {models.ErrNotFound, "Auto-update policy not found"},
	"setting":            {models.ErrNotFound, "Setting not found"},
	"user":               {models.ErrNotFound, "User not found"},
	"session":            {models.ErrNotFound, "Session not found"},
}

// handleDBError routes an error from an internal/database getter: an absence to
// its 404 (code and message from notFoundWire, one place for all of them) and
// anything else to a 500 carrying THIS route's own diagnostic message and the
// cause.
//
// faultMsg is deliberately per-route and not centralised with the 404: the 404
// depends only on which entity was absent, while "Failed to load stack" vs
// "Failed to load backup run" is what tells an operator which call failed. It is
// also the pre-existing message, so the 500 body is unchanged by the collapse.
//
// The split itself is why this exists (agent-os-7lg1 and its dozen siblings): a
// getter that could not answer used to arrive at these call sites looking exactly
// like an absent row, and roughly a dozen of them answered 404 for a database
// fault. Now only internal/database can mint the absence.
func handleDBError(c *gin.Context, err error, faultMsg string) {
	handleError(c, dbError(err, faultMsg))
}

// dbError is handleDBError's decision without the write, for a helper that
// returns its error to a caller which passes it to handleError (git.go's
// resolvePathFromStack). An absence is returned unchanged so handleError maps
// it through notFoundWire; anything else becomes this route's 500.
func dbError(err error, faultMsg string) error {
	if errors.Is(err, errdefs.ErrNotFound) {
		return err
	}
	return models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", faultMsg, err)
}

func handleError(c *gin.Context, err error) {
	var appErr *models.AppError
	if errors.As(err, &appErr) {
		if routineErrorCodes[appErr.Code] {
			middleware.MarkRoutineOutcome(c)
		}
		logServerFault(c, appErr.Status, appErr.Code, err)
		c.JSON(appErr.Status, appErr)
		return
	}

	// An absence from internal/database. Deliberately AFTER the AppError branch:
	// a handler that wraps one in an explicit AppError has made a decision this
	// must not override.
	//
	// No middleware.MarkRoutineOutcome here, and that is the pre-existing
	// behaviour rather than an omission: none of the collapsed routes marked
	// their 404 routine, and models.ErrNotFound must keep warning because the
	// same code answers genuine client errors elsewhere (see routineErrorCodes
	// above and env.go's per-site marker). logServerFault is likewise not called
	// — it returns early below 500, so a 404 never logged and still never does.
	var nf *errdefs.NotFoundError
	if errors.As(err, &nf) {
		wire, ok := notFoundWire[nf.Kind]
		if !ok {
			// A getter minted a Kind with no entry. Answer 404 rather than 500
			// (the fact is still "absent"), but say so in the log so the missing
			// row gets added instead of silently shipping a bare message.
			slog.Warn("no notFoundWire entry for kind", "kind", nf.Kind, "request_id", middleware.RequestIDFrom(c))
			wire.Code, wire.Message = models.ErrNotFound, "Not found"
		}
		c.JSON(http.StatusNotFound, models.NewAppError(http.StatusNotFound, wire.Code, wire.Message))
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
		renderResultWithStatus(c, http.StatusServiceUnavailable, truth.Failed(DockerUnavailableMessage, err))
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
