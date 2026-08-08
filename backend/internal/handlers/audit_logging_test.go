package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// auditEntries returns all action_log rows matching the given action name.
func auditEntries(t *testing.T, db *database.DB, action string) []struct {
	UserID string
	Detail string
} {
	t.Helper()
	rows, total, err := db.ListActionLogsFiltered(100, 0, database.ActionLogFilter{Action: action})
	require.NoError(t, err)
	out := make([]struct {
		UserID string
		Detail string
	}, 0, total)
	for _, r := range rows {
		out = append(out, struct {
			UserID string
			Detail string
		}{UserID: r.UserID, Detail: r.Detail})
	}
	return out
}

// Updating the global environment must be audited, but the audit detail must
// never leak the variable values (they routinely hold secrets) — only the count.
func TestUpdateGlobalEnv_WritesAuditEntry_WithoutSecretValues(t *testing.T) {
	handler, _ := newTestSettingsHandler(t)
	// The audit row's user_id references a real user (the route is auth-protected
	// in production); create that user and inject its ID into the request context.
	createTestUser(t, handler.db, "admin", "correct-horse-battery")
	router := gin.New()
	router.PUT("/settings/global-env", authContextMiddleware("test-user-id"), envUnlockedMiddleware(), handler.UpdateGlobalEnv)

	body := `{"vars":[{"key":"API_TOKEN","value":"s3cr3t-value"},{"key":"REGION","value":"eu-west"}]}`
	req := httptest.NewRequest(http.MethodPut, "/settings/global-env", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	entries := auditEntries(t, handler.db, services.ActionUpdateGlobalEnv)
	require.Len(t, entries, 1, "expected exactly one update_global_env audit entry")
	assert.Contains(t, entries[0].Detail, `"count":2`)
	assert.NotContains(t, entries[0].Detail, "s3cr3t-value", "audit detail must not contain env values")
	assert.NotContains(t, entries[0].Detail, "API_TOKEN", "audit detail must not contain env keys")
}

// A failed login attempt must be recorded so brute-force activity is auditable;
// the detail carries the attempted username and reason, never a password.
func TestLogin_Failed_WritesAuditEntry(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()
	createTestUser(t, db, "alice", "correct-horse-battery")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"alice","password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	entries := auditEntries(t, db, services.ActionLoginFailed)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Detail, "alice")
	assert.Contains(t, entries[0].Detail, "invalid_password")
	assert.NotContains(t, entries[0].Detail, "wrong-password", "audit detail must not contain the attempted password")
}

// A successful login must be recorded with the authenticated user's ID.
func TestLogin_Success_WritesAuditEntry(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()
	createTestUser(t, db, "bob", "correct-horse-battery")

	handler := NewAuthHandler(db, "test-secret-key-32-chars", false)
	router := setupTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"bob","password":"correct-horse-battery"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	entries := auditEntries(t, db, services.ActionLogin)
	require.Len(t, entries, 1)
	assert.NotEmpty(t, entries[0].UserID)
	assert.Contains(t, entries[0].Detail, "bob")
}
