package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// runEnvUnlock passes req through EnvUnlock for userID and returns the
// context the handler would see.
func runEnvUnlock(t *testing.T, store *services.EnvUnlockStore, userID string, req *http.Request) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var seen *gin.Context
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID); c.Next() })
	r.Use(EnvUnlock(store, false))
	r.GET("/x", func(c *gin.Context) { seen = c.Copy() })
	r.ServeHTTP(httptest.NewRecorder(), req)
	require.NotNil(t, seen)
	return seen
}

func upgradeRequest(protocols string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Protocol", protocols)
	return req
}

func checkOf(t *testing.T, c *gin.Context) EnvUnlockCheck {
	t.Helper()
	v, ok := c.Get(CtxEnvUnlockCheck)
	require.True(t, ok, "EnvUnlock published no check")
	check, ok := v.(EnvUnlockCheck)
	require.True(t, ok)
	return check
}

// agent-os-rbmi: a browser cannot set X-Unlock-Token on a WebSocket upgrade,
// so the token travels as a subprotocol and unlocks exactly as the header does.
func TestEnvUnlock_SubprotocolTokenUnlocksAnUpgrade(t *testing.T) {
	store := services.NewEnvUnlockStore()
	token, _, err := store.Mint("u1")
	require.NoError(t, err)

	c := runEnvUnlock(t, store, "u1", upgradeRequest(WSSubprotocol+", "+EnvUnlockSubprotocolPrefix+token))
	require.True(t, c.GetBool(CtxEnvUnlocked))
	require.Equal(t, token, c.GetString(CtxEnvUnlockToken))
}

// The subprotocol is read on an upgrade only; a plain request uses the header.
func TestEnvUnlock_SubprotocolIgnoredOnAPlainRequest(t *testing.T) {
	store := services.NewEnvUnlockStore()
	token, _, err := store.Mint("u1")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Sec-WebSocket-Protocol", EnvUnlockSubprotocolPrefix+token)
	c := runEnvUnlock(t, store, "u1", req)
	require.False(t, c.GetBool(CtxEnvUnlocked))
}

// Another user's token unlocks nothing, by either route, and the published
// check (what a socket re-checks and renews with) is bound to the same user.
func TestEnvUnlock_TokenAndCheckBoundToTheUser(t *testing.T) {
	store := services.NewEnvUnlockStore()
	other, _, err := store.Mint("u2")
	require.NoError(t, err)
	mine, _, err := store.Mint("u1")
	require.NoError(t, err)

	c := runEnvUnlock(t, store, "u1", upgradeRequest(WSSubprotocol+", "+EnvUnlockSubprotocolPrefix+other))
	require.False(t, c.GetBool(CtxEnvUnlocked))
	require.Empty(t, c.GetString(CtxEnvUnlockToken))

	check := checkOf(t, c)
	require.False(t, check(other))
	require.True(t, check(mine))
	store.RevokeUser("u1")
	require.False(t, check(mine), "the check must follow the store, not a verdict taken at the request")
}

func TestEnvUnlock_AuthDisabledCheckIsOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var seen *gin.Context
	r := gin.New()
	r.Use(EnvUnlock(nil, true))
	r.GET("/x", func(c *gin.Context) { seen = c.Copy() })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	require.True(t, seen.GetBool(CtxEnvUnlocked))
	require.True(t, checkOf(t, seen)(""))
}
