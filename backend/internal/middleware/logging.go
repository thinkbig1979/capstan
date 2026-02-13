package middleware

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		if path == "/health" {
			c.Next()
			return
		}

		c.Next()

		duration := time.Since(start)

		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			authHeader = "Bearer ***"
		}

		level := slog.LevelInfo
		if c.Writer.Status() >= 400 && c.Writer.Status() < 500 {
			level = slog.LevelWarn
		} else if c.Writer.Status() >= 500 {
			level = slog.LevelError
		}

		slog.Log(c.Request.Context(), level, "HTTP request",
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
