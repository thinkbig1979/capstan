package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// loopbackHosts are always accepted: they are the names a browser on the
// Capstan host itself uses, and no public DNS name can be one of them.
var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

// AllowedHosts closes DNS rebinding while AUTH_DISABLED is on
// (agent-os-n4ca.1). AuthMiddleware's bypass trusts any loopback peer, and a
// page served from an attacker's hostname that later resolves to 127.0.0.1
// connects FROM loopback, so the peer address cannot tell it apart from the
// operator's own browser. The Host header can: the browser keeps sending the
// attacker's name. So with auth disabled, a request whose Host is not a
// loopback name or listed in ALLOWED_HOSTS gets 403 before any route runs,
// WebSocket upgrades and the SPA index included.
//
// With auth enabled it does nothing: every protected route needs a session
// token whatever name the browser used, and the login cookie is not sent to
// a foreign hostname.
//
// Only r.Host is read. Host is a forbidden request header (Fetch spec), so a
// page cannot change it; X-Forwarded-Host is not, and a rebound page is
// same-origin, so it may set that header freely. Honouring it would hand the
// attacker the allowed name.
func AllowedHosts(authDisabled bool, allowedHosts []string) gin.HandlerFunc {
	if !authDisabled {
		return func(c *gin.Context) { c.Next() }
	}

	allowed := make(map[string]struct{}, len(loopbackHosts)+len(allowedHosts))
	for _, h := range append(append([]string{}, loopbackHosts...), allowedHosts...) {
		if n := normalizeHost(h); n != "" {
			allowed[n] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		host := normalizeHost(c.Request.Host)
		if _, ok := allowed[host]; ok && host != "" {
			c.Next()
			return
		}
		shown := host
		if shown == "" {
			shown = c.Request.Host
		}
		slog.Warn("Request refused: Host not allowed while authentication is disabled",
			"host", c.Request.Host, "ip", c.RemoteIP())
		c.JSON(http.StatusForbidden, models.NewAppError(http.StatusForbidden, models.ErrForbidden,
			fmt.Sprintf("Host %q is not allowed while authentication is disabled; add it to ALLOWED_HOSTS", shown)))
		c.Abort()
	}
}

// normalizeHost reduces a Host header or an ALLOWED_HOSTS entry to a bare,
// lowercase hostname: port dropped, IPv6 brackets dropped, one trailing dot
// dropped (DNS treats "localhost." and "localhost" as the same name). It
// returns "" for a malformed bracketed form, which never matches.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	switch {
	case strings.HasPrefix(h, "["):
		end := strings.Index(h, "]")
		if end < 0 {
			return ""
		}
		if rest := h[end+1:]; rest != "" && !strings.HasPrefix(rest, ":") {
			return ""
		}
		h = h[1:end]
	case strings.Count(h, ":") == 1:
		h = h[:strings.Index(h, ":")]
	}
	// Two or more colons without brackets is a bare IPv6 literal: kept whole.
	return strings.TrimSuffix(h, ".")
}
