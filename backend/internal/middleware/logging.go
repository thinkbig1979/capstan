package middleware

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RoutineOutcomeKey is the gin context key marking the current response as a
// ROUTINE negative answer — a true, expected fact about a resource — rather
// than a client error. LoggingMiddleware reads it to log such a 4xx at Info
// instead of Warn.
const RoutineOutcomeKey = "routineOutcome"

// MarkRoutineOutcome records that the response being written is a routine
// negative answer, so LoggingMiddleware does not log it at Warn.
//
// The motivating case (agent-os-prfj): on a host where no stack is git-backed,
// /git/log and /git/diff answer 404 GIT_NOT_REPO for every stack whose Activity
// tab is opened. "This directory is not a git repository" is the correct,
// expected answer to that question, not a client mistake — but
// LoggingMiddleware keyed level on status alone, so every one of those wrote a
// WARN line. Warning level stops being a signal when the normal case fills it.
//
// That example named GET /api/v1/git until agent-os-x40a, which went further on
// that ONE endpoint: rather than logging its routine 404 more quietly, it
// stopped calling the condition an error at all and answers 200
// `{"isRepo": false}`, because a 404 also put a red failed request in the
// browser console of every non-git stack. The endpoint is asked of every stack
// the frontend renders, so it was the loudest instance and is now not an
// instance at all. GIT_NOT_REPO is still minted, still routine, and still
// reaches this marker from the two paths above.
//
// EXPORTED, and deliberately not folded into handlers.handleError, because
// handleError is not the only way a 4xx leaves this server. handleError covers
// only responses that travel through it; the handlers package also writes 4xx
// directly with c.JSON. MEASURED at SHA 14d54a6:
//
//	command grep -rn "c.JSON(http.Status(NotFound|BadRequest|Unauthorized|Forbidden|Conflict|UnprocessableEntity)" backend/internal/handlers/*.go
//
// returns 122 such bare sites across 13 files, none of them reachable by a
// marker set inside handleError. Two of them are in class and are filed
// separately as agent-os-hjmf (handlers/env.go, "No env file associated with
// this stack"); they will call this function directly. Pin that 122 to its SHA
// — counts here go stale on every merge, so re-measure before quoting it.
//
// Callers decide routineness from the CODE they are answering with, never from
// the status or the request path. A path- or status-keyed rule would also
// silence a genuine "stack not found" 404, which arrives on the same endpoint
// with the same status and is a real client error worth logging.
func MarkRoutineOutcome(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(RoutineOutcomeKey, true)
}

// IsRoutineOutcome reports whether MarkRoutineOutcome ran for this request. It
// returns false when it did not, which is the safe default: an unmarked 4xx
// keeps the louder level it has always had.
func IsRoutineOutcome(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(RoutineOutcomeKey); ok {
		if routine, ok := v.(bool); ok {
			return routine
		}
	}
	return false
}

func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// Probes run on a timer; logging every one buries the real traffic.
		// Exact matches, not a prefix, so a real route under /health* still logs.
		if path == "/health" || path == "/health/ready" {
			c.Next()
			return
		}

		c.Next()

		duration := time.Since(start)

		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			authHeader = "Bearer ***"
		}

		// A 4xx is a warning only when it is a client ERROR. When the handler
		// marked it as a routine negative answer, it is an ordinary outcome and
		// logs at Info like any other — the line is still emitted, just not as
		// a warning (see MarkRoutineOutcome). The marker is read here and never
		// derived from the status or the path: those cannot tell "not a git
		// repository" from "stack not found", which arrive on the same endpoint
		// with the same 404.
		//
		// 5xx is deliberately outside the marker's reach. A server fault is a
		// server fault whoever answered it, and nothing a handler sets should
		// be able to mute one.
		level := slog.LevelInfo
		if c.Writer.Status() >= 400 && c.Writer.Status() < 500 && !IsRoutineOutcome(c) {
			level = slog.LevelWarn
		} else if c.Writer.Status() >= 500 {
			level = slog.LevelError
		}

		slog.Log(c.Request.Context(), level, "HTTP request",
			"request_id", RequestIDFrom(c),
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", duration.Milliseconds(),
			"client_ip", c.ClientIP(),
			"user_agent", c.Request.UserAgent(),
			"authorization", authHeader,
		)
	}
}
