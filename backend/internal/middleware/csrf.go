package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	csrfCookieName = "capstan_csrf"
	csrfHeaderName = "X-CSRF-Token"
	csrfTokenLen   = 32
)

func GenerateCSRFToken() string {
	b := make([]byte, csrfTokenLen)
	if _, err := rand.Read(b); err != nil {
		slog.Error("Failed to generate CSRF token", "error", err)
		return ""
	}
	return hex.EncodeToString(b)
}

func CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			existing, err := c.Cookie(csrfCookieName)
			if err != nil || existing == "" {
				token := GenerateCSRFToken()
				if token != "" {
					setCSRFCookie(c, token)
				}
			}
			c.Next()
			return
		}

		if isWebSocketUpgrade(c) {
			c.Next()
			return
		}

		if isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		csrfCookie, err := c.Cookie(csrfCookieName)
		if err != nil || csrfCookie == "" {
			slog.Warn("CSRF cookie missing on mutating request", "path", c.Request.URL.Path, "method", c.Request.Method)
			token := GenerateCSRFToken()
			if token != "" {
				setCSRFCookie(c, token)
			}
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "CSRF_COOKIE_MISSING",
				"message": "CSRF cookie required. Reload the page and retry.",
			})
			c.Abort()
			return
		}

		csrfHeader := c.GetHeader(csrfHeaderName)
		if csrfHeader == "" {
			slog.Warn("CSRF token missing in header", "path", c.Request.URL.Path, "method", c.Request.Method)
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "CSRF_TOKEN_MISSING",
				"message": "CSRF token required",
			})
			c.Abort()
			return
		}

		if !strings.EqualFold(csrfCookie, csrfHeader) {
			slog.Warn("CSRF token mismatch", "path", c.Request.URL.Path, "method", c.Request.Method)
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "CSRF_TOKEN_INVALID",
				"message": "CSRF token mismatch",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func setCSRFCookie(c *gin.Context, token string) {
	secure := !strings.Contains(c.Request.Host, "localhost") && !strings.Contains(c.Request.Host, "127.0.0.1")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		MaxAge:   86400,
		Path:     "/",
		Secure:   secure,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
	c.Header(csrfHeaderName, token)
}

func isWebSocketUpgrade(c *gin.Context) bool {
	return strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
}

func isPublicPath(path string) bool {
	return IsPublicPath(path)
}
