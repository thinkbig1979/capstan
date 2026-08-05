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

		// N10 (agent-os-4pa.4 / agent-os-bns): this compare is intentionally NOT
		// constant-time, and must not be "fixed" to subtle.ConstantTimeCompare.
		// This is a double-submit check: both the cookie and the header are supplied
		// by the client on the same request, so neither side is a server-held secret
		// an attacker is trying to guess — there is nothing for a timing oracle to
		// leak. Cross-origin forgery is blocked upstream of this line regardless: the
		// session and CSRF cookies are SameSite=Lax (not sent on cross-site
		// subrequests), and the CSRF cookie, though HttpOnly:false so the same-origin
		// SPA can read it for the double-submit, cannot be read by cross-origin JS
		// (same-origin policy). A CORS middleware DOES exist (CORSMiddleware in this
		// package), but it is an exact-match origin allowlist, empty by default, and
		// does not weaken any of the above. EqualFold is used on purpose: the token
		// is lowercase hex, so accepting case variants of the same token loses no
		// entropy, whereas subtle.ConstantTimeCompare would make the compare
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
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124: Secure is conditional, set above from IsSecureRequest, which gosec can't evaluate; HttpOnly is deliberately false so the same-origin SPA can read this CSRF cookie for the double-submit check — see comment at line 78-92 above
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
