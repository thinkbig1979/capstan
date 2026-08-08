package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// EnvUnlockHeader carries the token minted by POST /auth/verify-password. A
// header rather than a query parameter so the token never reaches access logs,
// browser history or a Referer.
const EnvUnlockHeader = "X-Unlock-Token"

// CtxEnvUnlocked is the gin context key this middleware publishes. Handlers read
// it with c.GetBool, so a route that never passed through this middleware reads
// false and therefore fails closed.
const CtxEnvUnlocked = "envUnlocked"

// EnvUnlock validates the X-Unlock-Token header against the store and publishes
// the verdict for the secret-reveal handlers to read. It never rejects a
// request: an absent or stale token is a normal state that yields a redacted
// response, not an error.
//
// When authentication is disabled there is no password to re-check, so a second
// factor cannot exist and the gate is open. That is deliberate: AUTH_DISABLED is
// already documented as trusted-network-only, and failing closed there would
// lock an operator out of their own env files with no way to unlock.
func EnvUnlock(store *services.EnvUnlockStore, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authDisabled {
			c.Set(CtxEnvUnlocked, true)
			c.Next()
			return
		}

		if store != nil {
			token := c.GetHeader(EnvUnlockHeader)
			if store.Valid(token, c.GetString("userID")) {
				c.Set(CtxEnvUnlocked, true)
			}
		}

		c.Next()
	}
}
