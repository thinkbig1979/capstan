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

		// N10 (agent-os-4pa.4): this compare is intentionally NOT constant-time,
		// and must not be "fixed" to subtle.ConstantTimeCompare. The timing channel
		// is unreachable here: there is no CORS middleware in this service, so a
		// cross-origin request carrying a custom CSRF header never survives
		// preflight; the session cookies are SameSite=Lax; and the CSRF cookie is
		// deliberately HttpOnly:false (double-submit) so a same-origin attacker who
		// can vary the header already reads the cookie directly. Neither side of the
		// compare is a secret the attacker lacks. EqualFold is used on purpose: the
		// token is lowercase hex, so accepting case variants of the same token loses
		// no entropy, and subtle.ConstantTimeCompare would instead make the compare
		// case-sensitive — a behaviour change for zero security gain.
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

// IsSecureRequest reports whether the request reached the server over HTTPS,
// either directly (TLS) or via a TLS-terminating reverse proxy that sets
// X-Forwarded-Proto. It is the basis for the Secure cookie flag and HSTS, and
// replaces the previous fragile Host-substring heuristic which could be tricked
// by a Host like "localhost.evil.com" into dropping Secure (M3).
func IsSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

func setCSRFCookie(c *gin.Context, token string) {
	secure := IsSecureRequest(c)
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
