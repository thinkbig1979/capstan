package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Proxy trust decides which address Capstan treats as the client, and getting it
// wrong is silent in both directions: a proxy that is not trusted collapses every
// user onto one address (shared rate limit bucket), and a proxy that forwards a
// client-supplied X-Forwarded-For instead of overwriting it lets a caller choose
// their own apparent address. This file makes both states visible in the log.
// AUTH_DISABLED is unaffected by any of this — it checks the real socket peer,
// never a forwarded header (agent-os-0s4, see middleware/auth.go).

// untrustedProxyWarnLimit caps how many distinct peers are remembered for
// warn-once purposes. Beyond it, warnings stop rather than the map growing on
// attacker-chosen input — this is a misconfiguration signal, not an audit trail,
// so the first few are the informative ones.
const untrustedProxyWarnLimit = 64

var untrustedProxyWarned struct {
	mu    sync.Mutex
	peers map[string]struct{}
}

// LogTrustedProxies records the effective trusted-proxy configuration at
// startup. Whether the list came from TRUSTED_NETWORKS or from the localhost
// default is the single most useful fact when diagnosing "logins are randomly
// refused" or "auth was skipped for a request I don't recognise".
func LogTrustedProxies(proxies []string, fromConfig bool) {
	source := "default"
	if fromConfig {
		source = "TRUSTED_NETWORKS"
	}
	slog.Info("Trusted proxy configuration",
		"source", source,
		"proxies", strings.Join(proxies, ","),
		"note", "X-Forwarded-For is only honored from these addresses")
}

// TrustedProxyWarning warns when a request arrives with a forwarding header from
// a peer that is not a trusted proxy. That combination means the header is being
// ignored and every user behind that proxy shares one apparent address, which
// otherwise fails silently.
func TrustedProxyWarning() gin.HandlerFunc {
	return func(c *gin.Context) {
		if header, ok := forwardingHeader(c.Request); ok {
			// ClientIP() returns the peer address unchanged when the peer is not a
			// trusted proxy, which is exactly the misconfiguration signature.
			remote := c.RemoteIP()
			if remote != "" && c.ClientIP() == remote {
				warnUntrustedProxy(remote, header)
			}
		}
		c.Next()
	}
}

func forwardingHeader(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if r.Header.Get(h) != "" {
			return h, true
		}
	}
	return "", false
}

// warnUntrustedProxy logs at most once per peer address. A per-request warning
// on the auth path would be an easy way to flood the log.
func warnUntrustedProxy(remoteIP, header string) {
	untrustedProxyWarned.mu.Lock()
	if untrustedProxyWarned.peers == nil {
		untrustedProxyWarned.peers = make(map[string]struct{})
	}
	if _, seen := untrustedProxyWarned.peers[remoteIP]; seen {
		untrustedProxyWarned.mu.Unlock()
		return
	}
	if len(untrustedProxyWarned.peers) >= untrustedProxyWarnLimit {
		untrustedProxyWarned.mu.Unlock()
		return
	}
	untrustedProxyWarned.peers[remoteIP] = struct{}{}
	untrustedProxyWarned.mu.Unlock()

	slog.Warn("Forwarding header received from an untrusted peer - it is being ignored",
		"peer", remoteIP,
		"header", header,
		"effect", "all clients behind this proxy share one apparent IP for rate limiting",
		"fix", "add this address to TRUSTED_NETWORKS, and make sure the proxy overwrites the header rather than forwarding a client-supplied value")
}
