package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("Panic recovered",
					"error", err,
					"stack", string(debug.Stack()),
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)

				// http.ErrAbortHandler is a sentinel net/http checks for with
				// pointer/value identity (server.go:1940, "err != ErrAbortHandler"),
				// not errors.Is. Detecting via errors.Is (to also catch a wrapped
				// value) is fine, but the re-panic must carry the sentinel itself,
				// never the recovered value, or that identity check fails upstream
				// and net/http dumps a full stack trace instead of quietly
				// aborting the connection.
				if err, ok := err.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(http.ErrAbortHandler)
				}

				// gin's responseWriter.WriteHeader (response_writer.go:65-72)
				// refuses to change the status once it has been committed, so a
				// handler that already wrote a response has permanently fixed the
				// status code before this recover ever runs. Writing another body
				// here cannot "correct" anything - it can only append a second,
				// malformed payload after the first. Once headers are written the
				// only safe move is to log and abort with no further write; the
				// bytes already handed to net/http cannot be recalled.
				if c.Writer.Written() {
					c.Abort()
					return
				}

				c.JSON(http.StatusInternalServerError, models.NewAppError(
					http.StatusInternalServerError,
					"INTERNAL_ERROR",
					"Internal server error",
				))
				c.Abort()
			}
		}()

		c.Next()
	}
}
