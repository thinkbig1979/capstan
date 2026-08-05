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

// trustedProxyNetworks holds the effective trusted-proxy network list used to
// decide whether X-Forwarded-Proto is honoured (agent-os-ab9). It is set once
// at startup by InitTrustedProxyNetworks from the SAME effective list handed
// to gin's SetTrustedProxies in main.go — gin's trusted-proxy machinery only
// governs X-Forwarded-For/X-Real-IP (RemoteIPHeaders), it never touches
// X-Forwarded-Proto, so without this the header was honoured from any peer
// whatsoever. Feeding both from one computed list means the two "which peers
// do we trust" answers cannot disagree.
var trustedProxyNetworks struct {
	mu       sync.RWMutex
	networks string
}

// InitTrustedProxyNetworks records the effective trusted-proxy network list.
// Call once at startup with the same []string passed to gin's
// SetTrustedProxies (see middleware.LogTrustedProxies, an existing precedent
// for startup-set middleware state). A nil or empty slice clears it, which
// leaves only the unconditional loopback trust that IsTrustedIP already
// grants.
func InitTrustedProxyNetworks(networks []string) {
	trustedProxyNetworks.mu.Lock()
	trustedProxyNetworks.networks = strings.Join(networks, ",")
	trustedProxyNetworks.mu.Unlock()
}

// isTrustedProxyPeer reports whether remoteIP is allowed to influence
// IsSecureRequest via X-Forwarded-Proto. Delegates to IsTrustedIP
// (internal/middleware/auth.go) rather than a second trust evaluator — two
// implementations of "is this peer trusted" that can disagree is its own
// defect.
func isTrustedProxyPeer(remoteIP string) bool {
	trustedProxyNetworks.mu.RLock()
	networks := trustedProxyNetworks.networks
	trustedProxyNetworks.mu.RUnlock()
	return IsTrustedIP(remoteIP, networks)
}

// untrustedForwardedProtoWarnLimit mirrors untrustedProxyWarnLimit above: cap
// distinct peers remembered so attacker-chosen input can't grow the map
// without bound.
const untrustedForwardedProtoWarnLimit = 64

var untrustedForwardedProtoWarned struct {
	mu    sync.Mutex
	peers map[string]struct{}
}

// warnUntrustedForwardedProto logs at most once per peer when X-Forwarded-Proto
// arrives from a peer outside the trusted-proxy list. This is a SEPARATE
// warn-once map from untrustedProxyWarned above rather than a shared one:
// TrustedProxyWarning fires only when ClientIP()==RemoteIP() (gin found no
// trusted proxy to resolve XFF through), a check that doesn't apply here —
// X-Forwarded-Proto isn't part of gin's RemoteIPHeaders at all, so this path
// must evaluate peer trust independently of gin's resolution, and the two
// warnings describe different consequences (rate-limit bucket collapse vs.
// cookie/HSTS security) that are each worth their own message and own budget.
func warnUntrustedForwardedProto(remoteIP string) {
	untrustedForwardedProtoWarned.mu.Lock()
	if untrustedForwardedProtoWarned.peers == nil {
		untrustedForwardedProtoWarned.peers = make(map[string]struct{})
	}
	if _, seen := untrustedForwardedProtoWarned.peers[remoteIP]; seen {
		untrustedForwardedProtoWarned.mu.Unlock()
		return
	}
	if len(untrustedForwardedProtoWarned.peers) >= untrustedForwardedProtoWarnLimit {
		untrustedForwardedProtoWarned.mu.Unlock()
		return
	}
	untrustedForwardedProtoWarned.peers[remoteIP] = struct{}{}
	untrustedForwardedProtoWarned.mu.Unlock()

	slog.Warn("X-Forwarded-Proto received from an untrusted peer - it is being ignored",
		"peer", remoteIP,
		"effect", "Secure cookie flag and HSTS are decided from the real connection instead (TLS only)",
		"fix", "add this address to TRUSTED_NETWORKS, and make sure the proxy overwrites the header rather than forwarding a client-supplied value")
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
