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
			// The allowlist means the response varies per request based on the
			// Origin header - without signaling that, a shared cache sitting in
			// front of this service could serve one origin's ACAO-bearing
			// response to a different origin.
			c.Header("Vary", "Origin")
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
