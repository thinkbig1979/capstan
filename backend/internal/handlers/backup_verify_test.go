package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// ─────────────────────────────────────────────
// POST /backups/verify (agent-os-j1jw)
// ─────────────────────────────────────────────

func TestRunVerify_AcceptsEmptyBody(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// See TestGetStatus_Shape: h.Stop must be registered last so it runs first.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	// Every field is optional, so no body at all is a legitimate request.
	req := httptest.NewRequest(http.MethodPost, "/api/backups/verify", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.NotEmpty(t, body["runId"])
	assert.Equal(t, "/ws/backups/verify/"+body["runId"].(string), body["wsUrl"])
}

func TestRunVerify_RejectsInvalidSubset(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/backups/verify",
		jsonBody(t, map[string]interface{}{"readDataSubset": "--read-data"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// 400 and not 202: the run row must not exist for a caller error.
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	assert.Empty(t, runs, "a refused verify request must not persist a run row")
}

// TestRunVerify_PersistsVerifyKind is the runtime half of migration 16's
// coverage: the CHECK constraint on backup_runs.kind rejects an unknown kind,
// so a missing migration would surface here as a launch failure rather than as
// a compile error.
func TestRunVerify_PersistsVerifyKind(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/backups/verify",
		jsonBody(t, map[string]interface{}{"readDataSubset": "1/12"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())

	runID := decodeBody(t, w)["runId"].(string)

	// h.Stop() (deferred above) is what guarantees the row is finalised, so the
	// kind is read here rather than the terminal status.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, "verify", run.Kind)
}

// ─────────────────────────────────────────────
// lastVerify on GET /backups/status — the WARN channel
// ─────────────────────────────────────────────

// TestGetStatus_LastVerifySurfacesFailure is the testable half of the design
// decision recorded in services.VerifyRepositoryData: a failing integrity check
// WARNS rather than blocks, and this endpoint is where the warning is visible.
func TestGetStatus_LastVerifySurfacesFailure(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID:           "run-verify-failed",
		Kind:         "verify",
		Trigger:      "manual",
		Status:       "failed",
		StartedAt:    "2026-02-01T00:00:00Z",
		ErrorMessage: "repository integrity check failed",
	}))
	// A LATER successful backup: lastVerify must not be masked by it, which is
	// exactly why it is reported separately from lastRun.
	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID:        "run-backup-ok",
		Kind:      "backup",
		Trigger:   "scheduled",
		Status:    "success",
		StartedAt: "2026-02-02T00:00:00Z",
	}))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	lastVerify, ok := body["lastVerify"].(map[string]interface{})
	require.True(t, ok, "status must carry a lastVerify object, got %v", body["lastVerify"])
	assert.Equal(t, "run-verify-failed", lastVerify["id"])
	assert.Equal(t, "failed", lastVerify["status"])

	// The newer backup is what lastRun reports, which is the masking this
	// separate field exists to avoid.
	lastRun, ok := body["lastRun"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "run-backup-ok", lastRun["id"])
}

func TestGetStatus_LastVerifyNullWhenNeverRun(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	_, hasKey := body["lastVerify"]
	assert.True(t, hasKey, "lastVerify must be present in the status response")
	assert.Nil(t, body["lastVerify"], "lastVerify must be null when no verification has run")
}
