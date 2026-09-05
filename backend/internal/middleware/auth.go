package middleware

import (
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

var PublicPaths = []string{
	"/api/v1/auth/login",
	"/api/v1/auth/setup",
	"/api/v1/auth/status",
	// Build identity is deliberately public: an uptime check or a support
	// conversation needs to answer "what is running here?" without a session.
	// This list is consulted by the CSRF middleware too, so keep the two in step.
	"/api/v1/version",
	// Liveness and readiness. Both carry their own network policy
	// (HEALTH_ALLOWED_NETWORKS, loopback always allowed) rather than a session.
	"/health",
	"/health/ready",
}

func IsPublicPath(path string) bool {
	for _, p := range PublicPaths {
		if path == p {
			return true
		}
	}
	return false
}

// IsTrustedIP reports whether clientIP is loopback or falls inside one of the
// comma-separated CIDRs (or literal addresses) in networks. Loopback is always
// allowed, and an empty list means loopback only.
//
// Two callers with deliberately different lists: the AUTH_DISABLED bypass uses
// AUTH_DISABLED_ALLOWED_NETWORKS, the health endpoints use
// HEALTH_ALLOWED_NETWORKS. Only the matching logic is shared — see
// config.Config.HealthNetworks and config.Config.AuthDisabledAllowedNetworks
// for why the lists are not.
//
// The two callers also deliberately disagree on where clientIP comes from.
// health.go passes gin's resolved c.ClientIP(), which honors X-Forwarded-For
// from a trusted proxy — fine for a read-only liveness/readiness check.
// AuthMiddleware instead passes c.RemoteIP(), the raw socket peer, ignoring
// X-Forwarded-For entirely: this loopback check is the AUTH_DISABLED admin
// bypass, and a resolved ClientIP() can be walked to "127.0.0.1" by an
// attacker forging X-Forwarded-For through a trusted proxy, which would
// satisfy the hardcoded loopback rule below unconditionally regardless of
// the networks list (agent-os-0s4, vector 2). RemoteIP() cannot be spoofed
// that way — see proxytrust.go for the same ClientIP()-vs-RemoteIP() split
// used to detect proxy misconfiguration.
func IsTrustedIP(clientIP string, networks string) bool {
	if clientIP == "127.0.0.1" || clientIP == "::1" || clientIP == "localhost" {
		return true
	}

	if networks == "" {
		return false
	}

	for _, networkStr := range strings.Split(networks, ",") {
		networkStr = strings.TrimSpace(networkStr)
		if networkStr == "" {
			continue
		}

		if networkStr == clientIP {
			return true
		}

		_, network, err := net.ParseCIDR(networkStr)
		if err != nil {
			// A bare IP (e.g. "127.0.0.1") is valid configuration - it just
			// isn't a range, and the exact-string comparison above already
			// covers it. Only warn when the entry is neither a valid IP nor
			// a valid CIDR, i.e. genuinely malformed, and only once per
			// distinct entry rather than once per call (agent-os-ab9):
			// networks is static config that doesn't change between
			// requests, so warning here on every call turns any per-request
			// caller (IsSecureRequest, via isTrustedProxyPeer in
			// proxytrust.go) into a log-flood amplifier - OBSERVED, 50
			// requests from one untrusted peer against the DEFAULT
			// "127.0.0.1,::1" list produced 100 of these lines before this
			// fix, one per bare-IP entry per call.
			if net.ParseIP(networkStr) == nil {
				warnInvalidTrustedNetworkOnce(networkStr, err)
			}
			continue
		}

		ip := net.ParseIP(clientIP)
		if ip == nil {
			continue
		}

		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// invalidTrustedNetworkWarnLimit caps how many distinct malformed entries are
// remembered for warn-once purposes. It is a plain lifetime cap with generous
// headroom, and it deliberately does NOT follow proxytrust.go's
// untrustedWarnBudget: that one keys on the peer address, which is
// attacker-chosen and unbounded, so it needs a per-window budget that refills
// (agent-os-coi). This one keys on entries of static operator config
// (TRUSTED_NETWORKS et al.), so its keyspace is bounded by the config file and
// a never-resetting cap cannot be exhausted by traffic. The two used to share
// a shape; since agent-os-coi they do not, and that divergence is intentional.
const invalidTrustedNetworkWarnLimit = 64

var invalidTrustedNetworkWarned struct {
	mu      sync.Mutex
	entries map[string]struct{}
}

// warnInvalidTrustedNetworkOnce logs at most once per distinct malformed
// networks entry. See the call site in IsTrustedIP for why a per-call
// warning here was a log-flood defect (agent-os-ab9).
func warnInvalidTrustedNetworkOnce(networkStr string, err error) {
	invalidTrustedNetworkWarned.mu.Lock()
	if invalidTrustedNetworkWarned.entries == nil {
		invalidTrustedNetworkWarned.entries = make(map[string]struct{})
	}
	if _, seen := invalidTrustedNetworkWarned.entries[networkStr]; seen {
		invalidTrustedNetworkWarned.mu.Unlock()
		return
	}
	if len(invalidTrustedNetworkWarned.entries) >= invalidTrustedNetworkWarnLimit {
		invalidTrustedNetworkWarned.mu.Unlock()
		return
	}
	invalidTrustedNetworkWarned.entries[networkStr] = struct{}{}
	invalidTrustedNetworkWarned.mu.Unlock()

	slog.Warn("Invalid trusted network entry - neither a valid IP nor a valid CIDR", "network", networkStr, "error", err)
}

// extractBearerToken returns the JWT from either the Authorization header
// or the capstan_token cookie. The ?token= query param is deliberately
// not accepted to keep tokens out of access logs and Referer headers.
func extractBearerToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if cookie, err := c.Cookie("capstan_token"); err == nil {
		return cookie
	}
	return ""
}

// authAllowedNetworks is the AUTH_DISABLED bypass allowlist
// (config.Config.AuthDisabledAllowedNetworks) — deliberately not
// TrustedNetworks/Gin's trusted-proxy list, see IsTrustedIP (agent-os-0s4).
func AuthMiddleware(db *database.DB, jwtSecret string, authDisabled bool, authAllowedNetworks string) gin.HandlerFunc {
	if authDisabled {
		slog.Warn("WARNING: AUTHENTICATION DISABLED - Only safe on trusted networks!")
	}

	return func(c *gin.Context) {
		if authDisabled {
			// RemoteIP(), not ClientIP(): this is the security-critical bypass
			// decision, so it must not be swayed by a spoofed X-Forwarded-For
			// even from a peer Gin otherwise trusts as a proxy. See IsTrustedIP.
			clientIP := c.RemoteIP()
			if !IsTrustedIP(clientIP, authAllowedNetworks) {
				slog.Warn("Untrusted IP attempt with auth disabled", "ip", clientIP, "auth_disabled_allowed_networks", authAllowedNetworks)
				c.JSON(403, models.NewAppError(403, "FORBIDDEN", "Authentication disabled - only local connections allowed"))
				c.Abort()
				return
			}
			c.Set("userID", "anonymous")
			c.Set("username", "admin")
			c.Next()
			return
		}

		if IsPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Every 401 below is session loss, so all of them carry
		// ErrSessionExpired — the frontend logs out on that code and nothing
		// else. See models/errors.go for the contract; the handlers that find
		// the session's user row gone mint it too.
		token := extractBearerToken(c)
		if token == "" {
			c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Missing authorization token"))
			c.Abort()
			return
		}

		claims, err := validateJWT(token, jwtSecret)
		if err != nil {
			// Classified by sentinel identity, not by error text: golang-jwt/v5
			// joins ErrTokenExpired into every past-"exp" failure, and a release
			// that rewords the message must not demote it to the generic
			// message (agent-os-ih0i). Status and code are the same on both
			// branches; only the message differs.
			if errors.Is(err, jwt.ErrTokenExpired) {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session expired"))
			} else {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Invalid authorization token"))
			}
			c.Abort()
			return
		}

		// A validly-signed token with no "jti" (or a non-string one) names no
		// session row, so it can never be found and revoked by logout. Before
		// this fix the type assertion had no else branch and simply skipped the
		// session/revocation lookup, admitting an unrevocable token exactly like
		// the missing-"sub" gap below (agent-os-bm6). Same treatment: reject
		// with SESSION_EXPIRED rather than silently bypassing the check
		// (agent-os-gm5).
		jti, ok := claims["jti"].(string)
		if !ok {
			c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Invalid authorization token"))
			c.Abort()
			return
		}
		session, err := db.GetSession(jti)
		if err != nil {
			// Only a genuinely missing row is session loss. Every other error
			// -- a closed, locked or unreadable database -- used to be
			// answered with this same silent 401, which made a transient fault
			// log EVERY authenticated operator out with nothing recorded
			// server-side: the widest member of agent-os-8tqd's class, since
			// this guard runs on every non-public request. The
			// `|| session == nil` arm this replaces was dead — GetSession
			// returns the bare Scan error and never (nil, nil)
			// (database/users.go).
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session not found or expired"))
				c.Abort()
				return
			}
			// handlers.handleError and handlers.logServerFault are
			// unreachable from here: handlers imports middleware, so the
			// reverse direction is an import cycle. This is logServerFault's
			// message and its attribute keys in its order (handlers/
			// respond.go), so an operator's log grep finds both the same way.
			appErr := models.NewAppErrorWithCause(500, "INTERNAL_ERROR", "Session lookup failed", err)
			slog.Error("request failed",
				"request_id", RequestIDFrom(c),
				"status", 500,
				"code", "INTERNAL_ERROR",
				"error", appErr,
				"cause", err,
			)
			c.JSON(500, appErr)
			c.Abort()
			return
		}
		if session.ExpiresAt.Before(time.Now()) {
			c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session expired"))
			c.Abort()
			return
		}

		// A validly-signed token with no "sub" (or a non-string one) names no
		// user. It is not the "wrong credential" case ErrUnauthorized means —
		// it's a token that cannot authenticate any session, so it gets the
		// same ErrSessionExpired treatment as every other guard failure above
		// rather than silently reaching handlers with userID unset (agent-os-bm6).
		userID, ok := claims["sub"].(string)
		if !ok {
			c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Invalid authorization token"))
			c.Abort()
			return
		}
		c.Set("userID", userID)

		// Publish the session id this request authenticated against, so handlers
		// that need to act on the session itself do not have to re-derive it from
		// the transport. Logout is the one that does: it used to re-read the JWT
		// from the Authorization header, which the browser never sends (App.tsx
		// registers `() => null` as getToken, so api.ts never sets the header),
		// so its revoke silently did nothing for every cookie-authenticated
		// logout while still returning 204 (agent-os-h9o). The jti is already
		// parsed and its session row already validated above, so republishing it
		// here is the single-source-of-truth fix — no second parse, and no way
		// for header-vs-cookie to change the outcome.
		c.Set("jti", jti)

		if username, ok := claims["username"].(string); ok {
			c.Set("username", username)
		}

		c.Next()
	}
}

// jwtIssuer must match handlers.jwtIssuer; tokens are required to carry this
// "iss" claim (L2).
const jwtIssuer = "capstan"

// validateJWT is the seam AuthMiddleware parses through. Production never
// reassigns it; auth_expiry_test.go swaps it to present the middleware with an
// expiry error whose text a library bump has changed, which the real parser
// cannot be made to produce from outside (agent-os-ih0i).
var validateJWT = ValidateJWT

// WithExpirationRequired: without it, a token that omits "exp" entirely
// parses as valid and never expires (the library only checks expiry when the
// claim is present). Every minting site in this repo already sets "exp"
// (handlers/auth.go:450, and every test fixture — verified by grepping every
// "MapClaims{" construction site in backend/, agent-os-gm5), so this closes
// the gap without changing behavior for any real token.
func ValidateJWT(token, secret string) (jwt.MapClaims, error) {
	parsedToken, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}, jwt.WithIssuer(jwtIssuer), jwt.WithExpirationRequired())

	if err != nil {
		return nil, err
	}

	if claims, ok := parsedToken.Claims.(jwt.MapClaims); ok && parsedToken.Valid {
		return claims, nil
	}

	return nil, jwt.ErrInvalidKey
}
