package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func CORSMiddleware(allowedOrigins string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		if allowedOrigins == "" {
			c.Next()
			return
		}

		// Everything from here on depends on the request's Origin header: an
		// allowlisted origin gets Access-Control-Allow-Origin echoed back, any
		// other origin does not. That absence is itself origin-dependent
		// content - a cache that stores the disallowed-origin miss keyed by
		// URL alone could later replay that no-ACAO response to a
		// legitimately allowlisted origin and break its cross-origin request.
		// So Vary: Origin belongs to both outcomes below, not only the
		// allowed one.
		c.Header("Vary", "Origin")

		allowedList := strings.Split(allowedOrigins, ",")
		isAllowed := false

		for _, allowed := range allowedList {
			allowed = strings.TrimSpace(allowed)
			if allowed == origin {
				isAllowed = true
				break
			}
		}

		if isAllowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, X-CSRF-Token")
			c.Header("Access-Control-Expose-Headers", "Content-Length, X-CSRF-Token")
			c.Header("Access-Control-Max-Age", "3600")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
