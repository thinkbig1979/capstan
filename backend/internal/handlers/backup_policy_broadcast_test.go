package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Both policy writers must broadcast backup_policy_changed, or a policy removed
// in one session stays enabled in every other open UI until a refetch
// (agent-os-oomh). Not parallel: the event bus is process-global, and a
// sequential test runs while every t.Parallel test in the package is paused, so
// no other handler can put a backup_policy_changed on the bus meanwhile.
func TestBackupPolicyWritersBroadcastPolicyChanged(t *testing.T) {
	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "s1")
	h := NewBackupHandler(buildBackupSvc(t, db, true, false), db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	events, settle, stop := captureBroadcasts(t)
	defer stop()

	count := func() int {
		settle(t)
		n := 0
		for _, e := range events() {
			if e.Type == "backup_policy_changed" {
				n++
			}
		}
		return n
	}

	put := httptest.NewRequest(http.MethodPut, "/api/backups/policies/stack/s1", strings.NewReader(`{"enabled":true,"stopPolicy":"hot"}`))
	put.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, put)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, count(), "upsertPolicy must broadcast once")

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/backups/policies/stack/s1", nil))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.Equal(t, 2, count(), "deletePolicy must broadcast once too")
}
