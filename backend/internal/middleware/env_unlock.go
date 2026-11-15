package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

// WSSubprotocol is the one subprotocol the server echoes on a WebSocket
// upgrade. A browser cannot set EnvUnlockHeader on an upgrade, so it offers
// the unlock token as a second subprotocol, EnvUnlockSubprotocolPrefix plus
// the token; the server must select a protocol the client offered, and it
// selects only this one, so the token never comes back in a response
// (agent-os-rbmi).
const WSSubprotocol = "capstan"

// EnvUnlockSubprotocolPrefix marks the subprotocol that carries an unlock
// token on a WebSocket upgrade. A header like EnvUnlockHeader, so it stays out
// of access logs and history the way a query parameter would not.
const EnvUnlockSubprotocolPrefix = "capstan-unlock."

// CtxEnvUnlockToken is the gin context key for the token that unlocked this
// request ("" when none did). A long-lived connection keeps it to re-check
// the unlock it was opened under (safe-defaults rule 15).
const CtxEnvUnlockToken = "envUnlockToken"

// CtxEnvUnlockCheck is the gin context key for an EnvUnlockCheck bound to
// this request's user. A route that never passed through this middleware has
// none, and a reader must then treat the session as locked.
const CtxEnvUnlockCheck = "envUnlockCheck"

// EnvUnlockCheck reports whether token is a live unlock token for the user
// the request was authenticated as. It is the same check this middleware
// makes on the request's own token, so a token re-checked later, or one sent
// in-band over a socket, is held to exactly the header's rule.
type EnvUnlockCheck func(token string) bool

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
			c.Set(CtxEnvUnlockCheck, EnvUnlockCheck(func(string) bool { return true }))
			c.Next()
			return
		}

		userID := c.GetString("userID")
		check := EnvUnlockCheck(func(token string) bool {
			return store != nil && store.Valid(token, userID)
		})
		c.Set(CtxEnvUnlockCheck, check)

		token := c.GetHeader(EnvUnlockHeader)
		if token == "" {
			token = subprotocolUnlockToken(c)
		}
		if check(token) {
			c.Set(CtxEnvUnlocked, true)
			c.Set(CtxEnvUnlockToken, token)
		}

		c.Next()
	}
}

// subprotocolUnlockToken returns the unlock token a WebSocket upgrade offered
// as a subprotocol, or "". Read only on an upgrade: a plain request carries
// the token in EnvUnlockHeader.
func subprotocolUnlockToken(c *gin.Context) string {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		return ""
	}
	for _, p := range websocket.Subprotocols(c.Request) {
		if token, ok := strings.CutPrefix(p, EnvUnlockSubprotocolPrefix); ok {
			return token
		}
	}
	return ""
}
