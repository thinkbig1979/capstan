package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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
