package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// realUnlockFor is the production middleware.EnvUnlock over store, for a
// request authenticated as userID and carrying token, so the test exercises the
// same check every protected route makes.
func realUnlockFor(store *services.EnvUnlockStore, userID, token string) gin.HandlerFunc {
	unlock := middleware.EnvUnlock(store, false)
	return func(c *gin.Context) {
		c.Set("userID", userID)
		c.Request.Header.Set(middleware.EnvUnlockHeader, token)
		unlock(c)
	}
}

// TestEnvLock_ManualLockRevokesServerSide_qxio: the Lock button used to clear
// only the browser's copy of the unlock token, so the server kept honouring it
// until its 5-minute TTL (agent-os-qxio). POST /auth/lock revokes it: the same
// token is then refused on a REST route gated by refuseLocked, and a terminal
// opened under it closes 4403 at its next re-check.
func TestEnvLock_ManualLockRevokesServerSide_qxio(t *testing.T) {
	shortRecheck(t)
	store := services.NewEnvUnlockStore()
	token, _, err := store.Mint("u1")
	require.NoError(t, err)
	unlock := realUnlockFor(store, "u1", token)

	term := dialLiveTerminal(t, unlock)
	term.requireOpen(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(unlock)
	auth := NewAuthHandler(nil, "test-secret-key-32-chars-long!!!", false)
	auth.SetEnvUnlockStore(store)
	auth.RegisterProtectedRoutes(router.Group("/api"))
	// The refusal every locked-gated REST route opens with.
	router.POST("/api/probe", func(c *gin.Context) {
		if refuseLocked(c, "probe", lockedEchoReason) {
			return
		}
		c.Status(http.StatusOK)
	})

	probe := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/probe", nil))
		return w
	}
	require.Equal(t, http.StatusOK, probe().Code, "the token unlocks before Lock")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/auth/lock", nil))
	require.Equal(t, http.StatusNoContent, w.Code, "POST /auth/lock: %s", w.Body.String())

	refused := probe()
	require.Equal(t, http.StatusForbidden, refused.Code, "the token still unlocks after Lock")
	var body models.AppError
	require.NoError(t, json.Unmarshal(refused.Body.Bytes(), &body))
	require.Equal(t, models.ErrEnvLocked, body.Code)

	term.requireClosedUnlockRequired(t)
}
