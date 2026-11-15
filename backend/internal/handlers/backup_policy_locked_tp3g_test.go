package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestBackupPolicy_LockedWritesRefused_tp3g: a backup policy decides which
// stacks are backed up, and a locked session could create, change or delete
// one (agent-os-tp3g). Locked, PUT and DELETE answer 403 ENV_LOCKED and leave
// the stored policy as it was; unlocked, both still work.
func TestBackupPolicy_LockedWritesRefused_tp3g(t *testing.T) {
	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "s1")
	h := NewBackupHandler(buildBackupSvc(t, db, true, false), db, slog.Default())
	t.Cleanup(h.Stop)
	locked := lockedBackupRouterH71i(h)
	unlocked := newBackupRouter(h)

	put := func(r http.Handler, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/backups/policies/stack/s1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	del := func(r http.Handler) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/backups/policies/stack/s1", nil))
		return w
	}
	stored := func() *models.BackupPolicy {
		p, err := db.GetBackupPolicy("s1")
		if errors.Is(err, errdefs.ErrNotFound) {
			return nil
		}
		require.NoError(t, err)
		return p
	}

	// Locked create: refused, no row.
	w := put(locked, `{"enabled":true,"stopPolicy":"hot"}`)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w))
	require.Nil(t, stored(), "a locked create stored a policy")

	// Unlocked create works.
	w = put(unlocked, `{"enabled":true,"stopPolicy":"hot"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Locked change and delete: refused, the policy stays as it was.
	w = put(locked, `{"enabled":false,"stopPolicy":"stop"}`)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w))
	w = del(locked)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w))
	p := stored()
	require.NotNil(t, p, "a locked delete removed the policy")
	require.True(t, p.Enabled)
	require.Equal(t, "hot", p.StopPolicy)

	// Unlocked delete works.
	w = del(unlocked)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.Nil(t, stored())
}
