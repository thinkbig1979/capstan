package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/docker-manager/backend/internal/models"
	"github.com/gin-gonic/gin"
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
