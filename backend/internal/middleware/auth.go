package middleware

import (
	"log/slog"
	"net"
	"strings"
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
// TRUSTED_NETWORKS, the health endpoints use HEALTH_ALLOWED_NETWORKS. Only the
// matching logic is shared — see config.Config.HealthNetworks for why the lists
// are not.
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
			slog.Warn("Invalid trusted network CIDR", "network", networkStr, "error", err)
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

func AuthMiddleware(db *database.DB, jwtSecret string, authDisabled bool, trustedNetworks string) gin.HandlerFunc {
	if authDisabled {
		slog.Warn("WARNING: AUTHENTICATION DISABLED - Only safe on trusted networks!")
	}

	return func(c *gin.Context) {
		if authDisabled {
			clientIP := c.ClientIP()
			if !IsTrustedIP(clientIP, trustedNetworks) {
				slog.Warn("Untrusted IP attempt with auth disabled", "ip", clientIP, "trusted_networks", trustedNetworks)
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
		// ErrSessionExpired — this guard is the sole source of that code, and
		// the frontend logs out on it and nothing else. See models/errors.go.
		token := extractBearerToken(c)
		if token == "" {
			c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Missing authorization token"))
			c.Abort()
			return
		}

		claims, err := ValidateJWT(token, jwtSecret)
		if err != nil {
			if strings.Contains(err.Error(), "expired") {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session expired"))
			} else {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Invalid authorization token"))
			}
			c.Abort()
			return
		}

		if jti, ok := claims["jti"].(string); ok {
			session, err := db.GetSession(jti)
			if err != nil || session == nil {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session not found or expired"))
				c.Abort()
				return
			}
			if session.ExpiresAt.Before(time.Now()) {
				c.JSON(401, models.NewAppError(401, models.ErrSessionExpired, "Session expired"))
				c.Abort()
				return
			}
		}

		if userID, ok := claims["sub"].(string); ok {
			c.Set("userID", userID)
		}

		if username, ok := claims["username"].(string); ok {
			c.Set("username", username)
		}

		c.Next()
	}
}

// jwtIssuer must match handlers.jwtIssuer; tokens are required to carry this
// "iss" claim (L2).
const jwtIssuer = "capstan"

func ValidateJWT(token, secret string) (jwt.MapClaims, error) {
	parsedToken, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}, jwt.WithIssuer(jwtIssuer))

	if err != nil {
		return nil, err
	}

	if claims, ok := parsedToken.Claims.(jwt.MapClaims); ok && parsedToken.Valid {
		return claims, nil
	}

	return nil, jwt.ErrInvalidKey
}
