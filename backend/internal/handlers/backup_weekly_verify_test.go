package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunVerify_RefusedWhileABackupOperationRuns (agent-os-ffaj): `restic
// check` locks the repository exclusively, so a manual check started during a
// backup would fail on the lock, raising a false "integrity check failed", or
// make the backup fail. It is refused with 409 before any run row exists.
// requireAvailable already did this before agent-os-ffaj, so this is a guard
// on existing behaviour that the check now depends on, not a new branch. The
// control arm is the same router once the lock is free: 202, and the row it
// writes carries the manual trigger (that half is new: LaunchVerify takes it).
func TestRunVerify_RefusedWhileABackupOperationRuns(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	svc.ForceSetBusy(true)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/backups/verify", nil))
	svc.ForceSetBusy(false)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "BACKUP_BUSY", decodeBody(t, w)["code"])
	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	assert.Empty(t, runs, "a refused check must not leave a run row")

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/backups/verify", nil))
	require.Equal(t, http.StatusAccepted, w.Code, "control: the same router accepts once the lock is free; body: %s", w.Body.String())
	run, err := db.GetBackupRunByID(decodeBody(t, w)["runId"].(string))
	require.NoError(t, err)
	assert.Equal(t, "manual", run.Trigger)
}

// TestBackupSettings_VerifyWeeklyRoundTrips: GET reports the weekly check as
// on when nothing is stored (D68.3: default ON), and a PUT of false is what a
// later GET returns.
func TestBackupSettings_VerifyWeeklyRoundTrips(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	get := func() interface{} {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		v, ok := decodeBody(t, w)["verifyWeekly"]
		require.True(t, ok, "GET /settings/backup must carry verifyWeekly")
		return v
	}

	assert.Equal(t, true, get(), "no stored row means on")

	for _, want := range []bool{false, true} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{"verifyWeekly": want}))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, want, decodeBody(t, w)["verifyWeekly"], "PUT echoes the saved value")
		assert.Equal(t, want, get())
	}
}
