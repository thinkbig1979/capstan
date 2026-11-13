package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// bcrypt refuses passwords over 72 bytes. Setup and password change used to
// pass such a password to it and answer 500 "Failed to hash password"
// (agent-os-orxo). Both must refuse it up front with the policy's 400.

const tooLongMsgOrxo = "Password must not exceed 72 bytes (accented and non-Latin characters count as 2-4 bytes each)"

var (
	pw72Orxo = "Aa1!" + strings.Repeat("x", 68)
	pw73Orxo = "Aa1!" + strings.Repeat("x", 69)
)

func doJSONOrxo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSetup_PasswordOver72Bytes_Is400Orxo(t *testing.T) {
	require.Len(t, pw73Orxo, 73)
	require.Len(t, pw72Orxo, 72)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := setupTestRouter(NewAuthHandler(db, "test-secret-key-32-chars", false))

	w := doJSONOrxo(r, http.MethodPost, "/auth/setup", `{"username":"admin","password":"`+pw73Orxo+`"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "73-byte password: body = %s", w.Body.String())
	body := decodeErrEnvelope(t, w.Body.Bytes())
	require.Equal(t, "VALIDATION_ERROR", body.Code)
	require.Equal(t, tooLongMsgOrxo, body.Message)
	n, err := db.UserCount()
	require.NoError(t, err)
	require.Equal(t, 0, n, "a refused setup must not create the account")

	w = doJSONOrxo(r, http.MethodPost, "/auth/setup", `{"username":"admin","password":"`+pw72Orxo+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "72-byte password: body = %s", w.Body.String())
	n, err = db.UserCount()
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestChangePassword_PasswordOver72Bytes_Is400Orxo(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	user := createTestUser(t, db, "admin", "OldPassword123!")

	h := NewSettingsHandler(db, "", dbFaultTestSecret, false, nil, nil)
	r := newSessionLookupRouter(user.ID, func(r *gin.Engine) { r.PUT("/auth/password", h.ChangePassword) })

	w := doJSONOrxo(r, http.MethodPut, "/auth/password", `{"currentPassword":"OldPassword123!","newPassword":"`+pw73Orxo+`"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "73-byte password: body = %s", w.Body.String())
	body := decodeErrEnvelope(t, w.Body.Bytes())
	require.Equal(t, "VALIDATION_ERROR", body.Code)
	require.Equal(t, tooLongMsgOrxo, body.Message)

	w = doJSONOrxo(r, http.MethodPut, "/auth/password", `{"currentPassword":"OldPassword123!","newPassword":"`+pw72Orxo+`"}`)
	require.Equal(t, http.StatusNoContent, w.Code, "72-byte password: body = %s", w.Body.String())
}
