package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
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
func setupRealAuthChangePasswordRouter(t *testing.T, db *database.DB, secret string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewSettingsHandler(db, "", secret, false, nil, nil)
	router := gin.New()
	protected := router.Group("")
	protected.Use(middleware.AuthMiddleware(db, secret, false, ""))
	protected.PUT("/auth/password", handler.ChangePassword)
	return router
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

	router := setupRealAuthChangePasswordRouter(t, db, secret)

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
