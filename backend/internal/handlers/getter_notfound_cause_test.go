package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// This file pins agent-os-3h9x: the generalisation of agent-os-7lg1's class
// from db.GetStack to every DB getter whose error is mapped to a 404 without
// discriminating sql.ErrNoRows. Three sites carried it — backup.go's
// getRunDetail (GetBackupRunByID) and directories.go's UpdateCredentials and
// CredentialStatus (both GetDirectory).
//
// Unlike the updates.go site that ua4y_7lg1_cause_test.go documents, NONE of
// these three had an adjacent slog.Error beside the call, so a plain
// Contains(buf, "database is closed") IS a valid discriminator here: before
// the conversion the buffer is empty at these endpoints, which is the whole
// defect ("404 and no log; the fault is invisible"). The "cause=" assertion is
// kept as the primary one anyway, because it can only be satisfied by an
// AppError minted WithCause and so cannot be passed by any future adjacent
// log line that prints err without attaching it.
//
// Every faulty arm below is paired with a healthy arm on the SAME instrument,
// asserting the pre-existing 404 body survives byte-for-byte and logs nothing
// at ERROR. TestHandleError_LogCaptureControl (respond_test.go) is the
// positive control that captureHandlerLogs itself fires, so an empty buffer
// in the healthy arms is evidence of absence rather than a dead instrument.

// newFaultyBackupHandler builds a BackupHandler whose only wired dependency is
// a closed DB. getRunDetail touches h.db and nothing else, so the nil
// BackupService is never dereferenced on this path; h.Stop reaps the
// registry's gcLoop goroutine.
func newFaultyBackupHandler(t *testing.T) *gin.Engine {
	t.Helper()
	h := NewBackupHandler(nil, faultyDB(t), slog.Default())
	t.Cleanup(h.Stop)
	return newBackupRouter(h)
}

// newBackupRouterWithRequestID is newBackupRouter (backup_test.go) plus the
// RequestID middleware, so logServerFault's line carries the caller's
// X-Request-ID sentinel and an absence assertion can be about THIS request
// rather than about the shared sink (agent-os-nho7).
//
// A local twin rather than one line added to newBackupRouter: that helper is
// called from 50+ tests across 7 files, none of which asked for the extra
// middleware, and backup_test.go is outside this bead's FILES.
func newBackupRouterWithRequestID(h *BackupHandler) *gin.Engine {
	r := gin.New()
	r.Use(middleware.RequestID())
	h.RegisterRoutes(r.Group("/api"))
	return r
}

// TestGetRunDetail_DBFaultReturns500WithLoggedCause is the failing-first arm
// for backup.go's getRunDetail (GetBackupRunByID). Before the conversion a
// closed connection produced the identical 404 an absent run produces, with
// no log line at all.
func TestGetRunDetail_DBFaultReturns500WithLoggedCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	r := newFaultyBackupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/runs/any-run-id", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a closed DB is not a missing run and must not be reported as one; body=%s", w.Body.String())

	body := decodeBody(t, w)
	assert.Equal(t, "INTERNAL_ERROR", body["code"])
	assert.Equal(t, "Failed to load backup run", body["message"])

	assert.Contains(t, buf.String(), "cause=",
		"the AppError must be minted WithCause so logServerFault attaches the driver error; captured=%q", buf.String())
	assert.Contains(t, buf.String(), "database is closed",
		"the driver error text must reach the operator's log; captured=%q", buf.String())
	assert.NotContains(t, w.Body.String(), "database is closed",
		"the cause must never leak into the response body")
}

// TestGetRunDetail_HealthyMissingRunKeeps404 is the control on the same
// instrument: the not-found path must still answer with the exact 404 it
// answered with before, and must log nothing.
func TestGetRunDetail_HealthyMissingRunKeeps404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db := newBackupHandlerDB(t)
	h := NewBackupHandler(nil, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouterWithRequestID(h)

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodGet, "/api/backups/runs/unknown-run-id", nil)
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrNotFound, body["code"])
	assert.Equal(t, "Backup run not found", body["message"])
	requireNoOwnErrorLines(t, buf.String(), sentinel, "an ordinary missing run (not a server fault)")
}

// newDirectoriesRouter mounts the real route table so the two GetDirectory
// sites are reached through the same paths production uses.
//
// RequestID() runs the way it does in production, so logServerFault stamps
// the caller's X-Request-ID sentinel on its line and the healthy arms below
// can assert the absence of an ERROR line for THIS request rather than the
// absence of any ERROR line at all in the process-global sink (agent-os-nho7).
// This helper is local to this file, so adding it affects nothing else.
func newDirectoriesRouter(db *database.DB) *gin.Engine {
	r := gin.New()
	r.Use(middleware.RequestID())
	NewDirectoriesHandler(nil, db).RegisterRoutes(r.Group("/api/directories"))
	return r
}

// TestCredentialStatus_DBFaultReturns500WithLoggedCause is the failing-first
// arm for directories.go's CredentialStatus (GetDirectory).
//
// This site is the one the 3h9x brief nominated as its POSITIVE CONTROL, on
// the reading that it was already guarded. It was not: the
// errors.Is(err, sql.ErrNoRows) a few lines below it discriminates the NEXT
// call, GetDirectoryCredentials, and the -A6 sweep window reached that guard
// and mis-attributed it. The real always-guarded control is
// GetDirectoryCredentials itself, exercised by
// TestCredentialStatus_HealthyMissingDirectoryKeeps404's sibling coverage in
// directories_test.go, and untouched by this conversion.
func TestCredentialStatus_DBFaultReturns500WithLoggedCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	r := newDirectoriesRouter(faultyDB(t))

	req := httptest.NewRequest(http.MethodGet, "/api/directories/credential-status?path=/opt/stacks/whatever", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a closed DB is not a missing directory; body=%s", w.Body.String())

	body := decodeBody(t, w)
	assert.Equal(t, "INTERNAL_ERROR", body["code"])
	assert.Equal(t, "Failed to load directory", body["message"])

	assert.Contains(t, buf.String(), "cause=",
		"captured=%q", buf.String())
	assert.Contains(t, buf.String(), "database is closed",
		"captured=%q", buf.String())
	assert.NotContains(t, w.Body.String(), "database is closed")
}

// TestCredentialStatus_HealthyMissingDirectoryKeeps404 is its control.
func TestCredentialStatus_HealthyMissingDirectoryKeeps404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	r := newDirectoriesRouter(db)

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodGet, "/api/directories/credential-status?path=/opt/stacks/absent", nil)
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrNotFound, body["code"])
	assert.Equal(t, "Directory not found", body["message"])
	requireNoOwnErrorLines(t, buf.String(), sentinel, "an ordinary missing directory on CredentialStatus")
}

// TestUpdateCredentials_DBFaultReturns500WithLoggedCause is the failing-first
// arm for directories.go's UpdateCredentials (the first of the file's two
// GetDirectory reads; the second, the post-update re-read, already returned
// 500 with its cause and is untouched).
func TestUpdateCredentials_DBFaultReturns500WithLoggedCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	r := newDirectoriesRouter(faultyDB(t))

	req := httptest.NewRequest(http.MethodPut, "/api/directories/credentials",
		strings.NewReader(`{"path":"/opt/stacks/whatever","authType":"ssh"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"body=%s", w.Body.String())

	body := decodeBody(t, w)
	assert.Equal(t, "INTERNAL_ERROR", body["code"])
	assert.Equal(t, "Failed to load directory", body["message"])

	assert.Contains(t, buf.String(), "cause=", "captured=%q", buf.String())
	assert.Contains(t, buf.String(), "database is closed", "captured=%q", buf.String())
	assert.NotContains(t, w.Body.String(), "database is closed")
}

// TestUpdateCredentials_HealthyMissingDirectoryKeeps404 is its control.
func TestUpdateCredentials_HealthyMissingDirectoryKeeps404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	r := newDirectoriesRouter(db)

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodPut, "/api/directories/credentials",
		strings.NewReader(`{"path":"/opt/stacks/absent","authType":"ssh"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrNotFound, body["code"])
	assert.Equal(t, "Directory not found", body["message"])
	requireNoOwnErrorLines(t, buf.String(), sentinel, "an ordinary missing directory on UpdateCredentials")
}
