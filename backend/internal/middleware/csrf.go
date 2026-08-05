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
//
// X-Forwarded-Proto is honoured ONLY when the request's real socket peer
// (c.RemoteIP(), not c.ClientIP() — ClientIP() returns the FORWARDED address
// once a peer is trusted, so using it here would be circular) is in the
// trusted-proxy list set at startup via InitTrustedProxyNetworks (see
// proxytrust.go). From any other peer the header is ignored entirely and the
// result falls back to the real connection (c.Request.TLS != nil), with a
// once-per-peer warning so a misconfigured deployment is visible in logs
// rather than silently downgrading cookies (agent-os-ab9). Before this gate,
// any peer whatsoever could set "X-Forwarded-Proto: https" over a plaintext
// connection and receive Secure cookies and a plaintext HSTS header (OBSERVED
// 2026-08-05 against a6e0a29).
//
// The protocol is taken from the last NON-EMPTY X-Forwarded-Proto value, not
// the first: a reverse proxy that appends to an existing header (rather than
// overwriting it) can leave a client-forged value ahead of its own genuine
// one, either as a second header line or comma-joined onto the same line.
// http.Header.Get/gin's GetHeader return only the first value/line, which an
// attacker-controlled leading value would then win in both directions -
// forging "http" ahead of a real "https" would drop Secure/HSTS on a
// genuinely-encrypted deployment, and forging "https" ahead of a real "http"
// would fabricate Secure/HSTS on a plaintext connection in the
// separate-header-lines wire shape. Taking the last value (and, within that
// value, the last comma-separated element) reflects what the terminating
// proxy actually appended. A trailing empty element or instance - e.g. a
// proxy that writes "https," or appends a blank header - is skipped rather
// than treated as an authoritative empty override of a real value earlier
// in the list (agent-os-qru.9, OBSERVED 2026-08-05: see
// TestIsSecureRequest_TrustsLastForwardedProtoValue).
func IsSecureRequest(c *gin.Context) bool {
	if c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}
	values := c.Request.Header.Values("X-Forwarded-Proto")
	if len(values) == 0 {
		return false
	}
	remoteIP := c.RemoteIP()
	if !isTrustedProxyPeer(remoteIP) {
		warnUntrustedForwardedProto(remoteIP)
		return false
	}
	return strings.EqualFold(lastForwardedProto(values), "https")
}

// lastForwardedProto returns the last non-empty (after TrimSpace) value
// across all X-Forwarded-Proto header instances, scanning each instance's
// comma-separated elements from the end, and falling back to the previous
// instance when an instance is entirely empty. Returns "" if every instance
// and every element within them is empty.
func lastForwardedProto(values []string) string {
	for i := len(values) - 1; i >= 0; i-- {
		parts := strings.Split(values[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			if v := strings.TrimSpace(parts[j]); v != "" {
				return v
			}
		}
	}
	return ""
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
