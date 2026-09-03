package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"golang.org/x/crypto/bcrypt"
)

// These tests exercise ChangePassword behind the REAL middleware.AuthMiddleware,
// not the authContextMiddleware stub (testhelpers_test.go:59-64) the other
// ChangePassword tests use. The stub sets only "userID"; it never sets "jti", so
// it cannot represent the browser's cookie-authenticated request and cannot see
// agent-os-xdn. The production browser sends NO Authorization header
// (App.tsx:58-63 registers `() => null` as getToken, so api.ts never sets it) —
// the same blind spot that hid agent-os-h9o.
//
// Every assertion is on the SESSION ROW, not on the 204. ChangePassword returns
// 204 whether or not it invalidates anything (settings.go:213), so a status
// assertion cannot distinguish "revoked the other sessions" from "silently did
// nothing".
func setupRealAuthChangePasswordRouter(t *testing.T, db *database.DB, secret string) (*gin.Engine, *SettingsHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewSettingsHandler(db, "", secret, false, nil, nil)
	router := gin.New()
	protected := router.Group("")
	protected.Use(middleware.AuthMiddleware(db, secret, false, ""))
	protected.PUT("/auth/password", handler.ChangePassword)
	return router, handler
}

// seedSession creates a live session row for an existing user.
func seedSession(t *testing.T, db *database.DB, userID, jti string) {
	t.Helper()
	require.NoError(t, db.CreateSession(models.Session{
		ID:        jti,
		UserID:    userID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}))
}

// seedNamedUser creates a user with an explicit ID/username so a second,
// unrelated user can exist alongside the createTestUser default ("test-user-id").
func seedNamedUser(t *testing.T, db *database.DB, id, username, password string) models.User {
	t.Helper()
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	user := models.User{
		ID:        id,
		Username:  username,
		Password:  string(hashed),
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}
	require.NoError(t, db.CreateUser(user))
	return user
}

// TestSettingsHandler_ChangePassword_CookieOnlyRevokesOtherSessions is the
// regression test for agent-os-xdn. It reproduces the browser's request exactly:
// authentication by the capstan_token cookie with NO Authorization header.
//
// Seen failing first against pre-fix code: currentSessionID was derived only from
// the (absent) Authorization header, so it stayed "", the guard at settings.go:207
// was false, DeleteSessionsByUserExcluding never ran, and the OTHER session row
// survived — the assertion below failed on its value (row still present), not on a
// compile error.
func TestSettingsHandler_ChangePassword_CookieOnlyRevokesOtherSessions(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!!"
	const callerJTI = "session-caller"
	const otherJTI = "session-other"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "pwuser", "OldPassword123!")
	seedSession(t, db, user.ID, callerJTI)
	seedSession(t, db, user.ID, otherJTI)

	// A second, unrelated user with a live session. The exposure warning on
	// agent-os-xdn requires proving DeleteSessionsByUserExcluding never reaches
	// across the user_id filter (users.go:79).
	otherUser := seedNamedUser(t, db, "other-user-id", "victim", "VictimPass123!")
	const bystanderJTI = "session-bystander"
	seedSession(t, db, otherUser.ID, bystanderJTI)

	router, _ := setupRealAuthChangePasswordRouter(t, db, secret)

	callerToken := generateTestToken(user.ID, user.Username, callerJTI, secret)
	body := `{"currentPassword":"OldPassword123!","newPassword":"NewPassword456!"}`
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "capstan_token", Value: callerToken})
	// Deliberately NO req.Header.Set("Authorization", ...) — that is the defect.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code,
		"cookie auth must reach ChangePassword; a 401 here means the middleware rejected the cookie, not that revocation failed")

	// The other session of the SAME user must be revoked.
	otherSession, err := db.GetSession(otherJTI)
	assert.Nil(t, otherSession,
		"changing the password must invalidate the user's OTHER sessions, but the row survived — "+
			"the classic 'change my password to kick the intruder out' does nothing (agent-os-xdn)")
	assert.Error(t, err, "GetSession should report the other session as absent after the password change")

	// The caller's own session must survive — do not log the user out of the tab
	// they just used. This is why the `if currentSessionID != ""` guard is
	// load-bearing: DeleteSessionsByUserExcluding(userID, "") would match id != ""
	// and delete every row including this one.
	callerSession, err := db.GetSession(callerJTI)
	assert.NotNil(t, callerSession,
		"the caller's own session must survive its own password change")
	assert.NoError(t, err)

	// A different user's session must be untouched.
	bystander, err := db.GetSession(bystanderJTI)
	assert.NotNil(t, bystander,
		"another user's session must never be revoked by this user's password change")
	assert.NoError(t, err)
}

// TestSettingsHandler_ChangePassword_ClosesOtherLiveConnections is the
// regression for agent-os-teop: revoking the other session ROWS never touched
// an already-open WebSocket for them, so the classic "change my password to
// kick the intruder out" left their live PTY/log stream running.
//
// Two-sided across BOTH ConnectionManagers (mirrors
// TestAuthHandler_Logout_ClosesLiveConnectionsForRevokedSession): the OTHER
// session's connections close in each manager, while the CALLER's own
// connection — same user, but the session that must survive its own password
// change per database.DeleteSessionsByUserExcluding's semantics — stays open.
//
// Seen failing first against pre-fix code (no CloseForUser, no call site in
// ChangePassword): the "other" connection assertions failed because those
// connections were never removed from either manager.
func TestSettingsHandler_ChangePassword_ClosesOtherLiveConnections(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!!"
	const callerJTI = "session-caller-ws"
	const otherJTI = "session-other-ws"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "pwuser-ws", "OldPassword123!")
	seedSession(t, db, user.ID, callerJTI)
	seedSession(t, db, user.ID, otherJTI)

	router, handler := setupRealAuthChangePasswordRouter(t, db, secret)

	sharedCM := NewConnectionManager(10)
	terminalCM := NewConnectionManager(5)
	handler.SetConnectionManagers(ConnectionManagers{sharedCM, terminalCM})

	callerConn := &Connection{ID: uuid.New().String(), UserID: user.ID, SessionID: callerJTI}
	otherConnShared := &Connection{ID: uuid.New().String(), UserID: user.ID, SessionID: otherJTI}
	otherConnTerminal := &Connection{ID: uuid.New().String(), UserID: user.ID, SessionID: otherJTI}
	require.NoError(t, sharedCM.Add(callerConn.ID, callerConn))
	require.NoError(t, sharedCM.Add(otherConnShared.ID, otherConnShared))
	require.NoError(t, terminalCM.Add(otherConnTerminal.ID, otherConnTerminal))

	callerToken := generateTestToken(user.ID, user.Username, callerJTI, secret)
	body := `{"currentPassword":"OldPassword123!","newPassword":"NewPassword456!"}`
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "capstan_token", Value: callerToken})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	_, present := sharedCM.Get(callerConn.ID)
	assert.True(t, present, "the caller's own connection must not be closed by its own password change")

	_, present = sharedCM.Get(otherConnShared.ID)
	assert.False(t, present, "another live session's connection must be closed in the shared manager")
	_, present = terminalCM.Get(otherConnTerminal.ID)
	assert.False(t, present, "another live session's connection must be closed in the terminal manager too")
}

// TestSettingsHandler_ChangePassword_RevokesEnvUnlock is the regression for
// agent-os-teop's fourth part: the env-unlock token is a second factor bound
// to userID only (services/env_unlock.go), never to a session, so revoking
// sessions above does nothing to it. Before this fix, RevokeUser was wired
// into AuthHandler.Logout only (agent-os-gm5-era) — SettingsHandler never
// received the store at all, so an attacker holding a live unlock token kept
// the ability to reveal plaintext secrets for up to EnvUnlockTTL after the
// owner changed the password specifically to lock them out.
//
// Seen failing first against pre-fix code (no envUnlock field/setter on
// SettingsHandler, no RevokeUser call in ChangePassword): store.Valid still
// returned true after the password change, since nothing had revoked it.
func TestSettingsHandler_ChangePassword_RevokesEnvUnlock(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!!"
	const callerJTI = "session-caller-unlock"

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "pwuser-unlock", "OldPassword123!")
	seedSession(t, db, user.ID, callerJTI)

	router, handler := setupRealAuthChangePasswordRouter(t, db, secret)

	store := services.NewEnvUnlockStore()
	handler.SetEnvUnlockStore(store)
	unlockToken, _, err := store.Mint(user.ID)
	require.NoError(t, err)
	require.True(t, store.Valid(unlockToken, user.ID),
		"sanity: a freshly minted unlock token must validate before the password change")

	callerToken := generateTestToken(user.ID, user.Username, callerJTI, secret)
	body := `{"currentPassword":"OldPassword123!","newPassword":"NewPassword456!"}`
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "capstan_token", Value: callerToken})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	assert.False(t, store.Valid(unlockToken, user.ID),
		"changing the password must revoke any live env-unlock token for the user, not just sessions (agent-os-teop)")
}
