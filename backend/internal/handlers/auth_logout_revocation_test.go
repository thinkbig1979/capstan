package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// These tests exercise Logout behind the REAL middleware.AuthMiddleware, not the
// header-only stub in setupTestRouterWithAuth (testhelpers_test.go:66-89). That
// distinction is the whole point: the stub reads only the Authorization header,
// so it cannot represent a cookie-authenticated request at all, and the existing
// TestAuthHandler_Logout_Success (auth_test.go:341) passes the header explicitly.
// The production browser never sends that header — App.tsx:58-63 registers
// `() => null` as getToken, so api.ts:71-75 never sets Authorization — which is
// how agent-os-h9o hid behind a green suite.
//
// Both tests assert on the SESSION ROW, not on the 204. Logout returns 204
// unconditionally (auth.go:334), so a status assertion cannot distinguish
// "revoked" from "silently did nothing".
func setupRealAuthLogoutRouter(t *testing.T, db *database.DB, secret string) (*gin.Engine, *AuthHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(db, secret, false)
	router := gin.New()
	protected := router.Group("")
	protected.Use(middleware.AuthMiddleware(db, secret, false, ""))
	protected.POST("/auth/logout", handler.Logout)
	return router, handler
}

// seedUserAndSession creates a user plus a live session row and returns a signed
// JWT whose jti names that row.
func seedUserAndSession(t *testing.T, db *database.DB, secret, jti string) (models.User, string) {
	t.Helper()
	user := createTestUser(t, db, "logoutuser", "password123")
	require.NoError(t, db.CreateSession(models.Session{
		ID:        jti,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}))
	return user, generateTestToken(user.ID, user.Username, jti, secret)
}

// TestAuthHandler_Logout_CookieOnlyRevokesSession is the regression test for
// agent-os-h9o. It reproduces exactly what the browser sends: authentication by
// the capstan_token cookie with NO Authorization header.
//
// Seen failing first against pre-fix code: the session row survived, so
// GetSession returned a non-nil row and the final assertion failed on its value
// (not a compile error) with "logout must revoke the session row named by the
// token's jti".
func TestAuthHandler_Logout_CookieOnlyRevokesSession(t *testing.T) {
	const secret = "test-secret-key-32-chars"
	const jti = "session-cookie-only"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, token := seedUserAndSession(t, db, secret, jti)
	router, _ := setupRealAuthLogoutRouter(t, db, secret)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "capstan_token", Value: token})
	// Deliberately NO req.Header.Set("Authorization", ...) — that is the defect.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code,
		"cookie auth must reach Logout; a 401 here means the middleware rejected the cookie, not that revocation failed")

	session, err := db.GetSession(jti)
	assert.Nil(t, session,
		"logout must revoke the session row named by the token's jti, but the row survived — "+
			"the token stays valid until natural expiry, so logout was client-side only (agent-os-h9o)")
	assert.Error(t, err, "GetSession should report the row as absent after logout")
}

// TestAuthHandler_Logout_BearerHeaderRevokesSession is the positive control. It
// passed BEFORE the fix as well, which is precisely why the bug survived: it is
// the only path the old code handled. Keeping it guards against a fix that
// repairs the cookie path by breaking the header path.
func TestAuthHandler_Logout_BearerHeaderRevokesSession(t *testing.T) {
	const secret = "test-secret-key-32-chars"
	const jti = "session-bearer"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, token := seedUserAndSession(t, db, secret, jti)
	router, _ := setupRealAuthLogoutRouter(t, db, secret)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)

	session, err := db.GetSession(jti)
	assert.Nil(t, session, "bearer-header logout must still revoke the session row")
	assert.Error(t, err)
}

// TestAuthHandler_Logout_ClosesLiveConnectionsForRevokedSession is the
// regression for agent-os-teop: revoking the session ROW at logout never
// touched an already-open WebSocket (terminal, logs, ...), because nothing
// re-validates a live connection after the initial upgrade — the row could be
// gone for hours while the attacker's PTY kept streaming.
//
// Two-sided on the same instrument, across BOTH ConnectionManagers
// cmd/server/main.go wires (agent-os-teop acceptance #1/#2): the revoked
// session's connection closes in EACH manager, and a connection for a
// different, live session stays open in EACH manager. A fix reaching only one
// manager, or one that closes every connection regardless of session, would
// each pass half of these assertions and fail the other half.
//
// Seen failing first against pre-fix code (no CloseForSession, no call site in
// Logout): both revoked-connection assertions failed because the connections
// were never removed from either manager.
func TestAuthHandler_Logout_ClosesLiveConnectionsForRevokedSession(t *testing.T) {
	const secret = "test-secret-key-32-chars"
	const jti = "session-to-revoke"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, token := seedUserAndSession(t, db, secret, jti)
	router, handler := setupRealAuthLogoutRouter(t, db, secret)

	// Mirrors main.go: a shared-cap manager and a lower-cap terminal manager.
	sharedCM := NewConnectionManager(10)
	terminalCM := NewConnectionManager(5)
	handler.SetConnectionManagers(ConnectionManagers{sharedCM, terminalCM})

	revokedInShared := &Connection{ID: uuid.New().String(), UserID: "logoutuser", SessionID: jti}
	revokedInTerminal := &Connection{ID: uuid.New().String(), UserID: "logoutuser", SessionID: jti}
	liveInShared := &Connection{ID: uuid.New().String(), UserID: "someone-else", SessionID: "session-not-revoked"}
	liveInTerminal := &Connection{ID: uuid.New().String(), UserID: "someone-else", SessionID: "session-not-revoked"}
	require.NoError(t, sharedCM.Add(revokedInShared.ID, revokedInShared))
	require.NoError(t, terminalCM.Add(revokedInTerminal.ID, revokedInTerminal))
	require.NoError(t, sharedCM.Add(liveInShared.ID, liveInShared))
	require.NoError(t, terminalCM.Add(liveInTerminal.ID, liveInTerminal))

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "capstan_token", Value: token})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	_, present := sharedCM.Get(revokedInShared.ID)
	assert.False(t, present, "logout must close the revoked session's connection in the shared manager")
	_, present = terminalCM.Get(revokedInTerminal.ID)
	assert.False(t, present,
		"logout must close the revoked session's connection in the terminal manager too — "+
			"a fix reaching only one manager silently leaves the highest-stakes connection (the PTY) open")

	_, present = sharedCM.Get(liveInShared.ID)
	assert.True(t, present, "a connection for a different, non-revoked session must stay open in the shared manager")
	_, present = terminalCM.Get(liveInTerminal.ID)
	assert.True(t, present, "a connection for a different, non-revoked session must stay open in the terminal manager")
}
