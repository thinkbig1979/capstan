package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// ─────────────────────────────────────────────
// Test infrastructure
// ─────────────────────────────────────────────

func newBackupHandlerDB(t *testing.T) *database.DB {
	t.Helper()
	// Use an encryptor-backed DB so sensitive settings (restic_password,
	// git_https_token) can be stored — the DB now refuses to persist secrets in
	// plaintext (L1).
	enc := services.NewTokenEncryptorOrDefault("", "test-secret-32-chars-padding-here")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// buildBackupSvc constructs a BackupService with fake commandRunners so that
// no real restic/rclone binaries are required. resticPresent/rclonePresent
// control whether the corresponding binary path is set (non-empty).
func buildBackupSvc(t *testing.T, db *database.DB, resticPresent, rclonePresent bool) *services.BackupService {
	t.Helper()

	cfg := &config.Config{
		DataDir:      t.TempDir(),
		StacksDir:    "/opt/stacks",
		AuthDisabled: true,
		JWTSecret:    "test-secret-32-chars-padding-here",
	}

	opLock := services.NewOperationLock()
	actions := services.NewActionLogger(db)

	svc := services.NewBackupService(cfg, db, &noopDocker{}, opLock, actions)

	resticBin := ""
	if resticPresent {
		resticBin = "/usr/bin/restic"
	}
	rcloneBin := ""
	if rclonePresent {
		rcloneBin = "/usr/bin/rclone"
	}
	svc.SetBins(resticBin, rcloneBin)

	return svc
}

// noopDocker is a minimal dockerStopper for handler tests where docker
// interactions are irrelevant. It satisfies the interface used internally by
// BackupService.
type noopDocker struct{}

func (n *noopDocker) StopVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.Success("stack stopped"), ""
}
func (n *noopDocker) StartVerified(stack models.Stack) (truth.ActionResult, string) {
	return truth.Success("stack running"), ""
}
func (n *noopDocker) Status(stack models.Stack) (string, []models.Container, error) {
	return "stopped", nil, nil
}

// newBackupRouter wires a BackupHandler onto a gin.Engine with all REST routes
// registered. It mirrors the pattern used in stacks_test.go.
func newBackupRouter(h *BackupHandler) *gin.Engine {
	r := gin.New()
	group := r.Group("/api")
	h.RegisterRoutes(group)
	return r
}

// seedHandlerStack inserts a directory + stack into db (no backup policy), so
// that stack-existence checks pass.
func seedHandlerStack(t *testing.T, db *database.DB, stackID string) {
	t.Helper()
	dir := models.Directory{
		Path:    "/opt/stacks/" + stackID,
		Name:    stackID,
		RootDir: "/opt/stacks",
	}
	require.NoError(t, db.UpsertDirectory(dir))
	stack := models.Stack{
		ID:          stackID,
		Directory:   "/opt/stacks/" + stackID,
		ProjectName: stackID,
		Status:      "stopped",
	}
	require.NoError(t, db.UpsertStack(stack))
}

// jsonBody builds an io.Reader from a map, as a JSON request body.
func jsonBody(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewReader(b)
}

func jsonReq(t *testing.T, method, path string, body interface{}) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, jsonBody(t, body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r
}

// decodeBody deserialises the recorder body into a map.
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// ─────────────────────────────────────────────
// getSettings
// ─────────────────────────────────────────────

func TestGetSettings_PasswordNeverInResponse(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	// Store a secret password.
	require.NoError(t, db.SetSetting("restic_password", "super-secret"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	// The literal secret must not appear anywhere in the response JSON.
	assert.NotContains(t, w.Body.String(), "super-secret",
		"raw password must never appear in the settings response")

	// "password" key must not appear at all.
	_, hasPasswordKey := body["password"]
	assert.False(t, hasPasswordKey, "password key must not be present in response")

	// hasPassword must be true since a password was stored.
	hasPassword, ok := body["hasPassword"].(bool)
	require.True(t, ok, "hasPassword must be a bool")
	assert.True(t, hasPassword, "hasPassword must be true when password is set")
}

func TestGetSettings_HasPasswordFalseWhenNotSet(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	hasPassword, ok := body["hasPassword"].(bool)
	require.True(t, ok)
	assert.False(t, hasPassword)
}

func TestGetSettings_ShapeContainsExpectedFields(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, true)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	expectedKeys := []string{
		"repository", "repositorySource", "hasPassword", "passwordSource",
		"keepDaily", "keepWeekly", "keepMonthly", "keepYearly",
		"autoPrune", "scheduleIntervalMinutes", "syncAfterBackup",
		"rcloneRemote", "rclonePath", "rcloneTransfers", "hostname",
		"resticAvailable", "rcloneAvailable", "repositoryInitialized",
	}
	for _, key := range expectedKeys {
		_, ok := body[key]
		assert.True(t, ok, "response must contain key %q", key)
	}
}

func TestGetSettings_RepositorySource_DB(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", "/db/repo"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	assert.Equal(t, "db", body["repositorySource"], "repositorySource must be 'db' when DB has a value")
	assert.Equal(t, "/db/repo", body["repository"], "repository must reflect the DB value")
}

func TestGetSettings_RepositorySource_Default(t *testing.T) {
	t.Parallel()

	// Neither DB nor env provides a repository value.
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	// buildBackupSvc uses a config.Config with DataDir = t.TempDir() and empty
	// ResticRepository, so the default path is computed from DataDir.
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	assert.Equal(t, "default", body["repositorySource"], "repositorySource must be 'default' when neither DB nor env set")
	// The returned repository must be the computed default (non-empty path ending in restic-repo).
	repo, _ := body["repository"].(string)
	assert.NotEmpty(t, repo, "repository must be non-empty (computed default)")
}

func TestGetSettings_PasswordSource_DB(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_password", "secret"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	assert.Equal(t, "db", body["passwordSource"], "passwordSource must be 'db' when DB has a password")
	hasPassword, _ := body["hasPassword"].(bool)
	assert.True(t, hasPassword, "hasPassword must be true when DB has a password")
}

func TestGetSettings_PasswordSource_Default(t *testing.T) {
	t.Parallel()

	// No DB password, no env password.
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false) // cfg.ResticPassword is ""
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	assert.Equal(t, "default", body["passwordSource"], "passwordSource must be 'default' when neither DB nor env has a password")
	hasPassword, _ := body["hasPassword"].(bool)
	assert.False(t, hasPassword, "hasPassword must be false when no password configured")
}

// ─────────────────────────────────────────────
// updateSettings
// ─────────────────────────────────────────────

func TestUpdateSettings_WritesSettings(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	repo := "/data/my-repo"
	keepDaily := 14
	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"repository": repo,
		"keepDaily":  keepDaily,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify DB was written.
	gotRepo, err := db.GetSetting("restic_repository")
	require.NoError(t, err)
	assert.Equal(t, repo, gotRepo)

	gotKeepDaily, err := db.GetSetting("backup_keep_daily")
	require.NoError(t, err)
	assert.Equal(t, "14", gotKeepDaily)
}

func TestUpdateSettings_EmptyPasswordIsNoOp(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	// Pre-seed a password.
	require.NoError(t, db.SetSetting("restic_password", "original"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	// Send update with empty password — must be a no-op.
	emptyPwd := ""
	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"password": &emptyPwd,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Password in DB must still be the original (decrypted via GetSetting).
	gotPwd, err := db.GetSetting("restic_password")
	require.NoError(t, err)
	assert.Equal(t, "original", gotPwd, "empty password in request must not overwrite existing password")
}

// TestUpdateSettings_NoEncryptionKey_ReturnsClearErrorNotPanic drives the
// exact scenario reported in agent-os-16m: AUTH_DISABLED=true with neither
// STORAGE_KEY nor JWT_SECRET set (services.NewTokenEncryptorOrDefault("", "")
// mirrors that startup path), then PUT /api/v1/settings/backup with a
// password. Before the fix this panicked with a nil-pointer dereference
// inside TokenEncryptor.Encrypt; it must instead come back as a clear,
// actionable 422 the operator can map to "set STORAGE_KEY" — not a panic and
// not an opaque 500.
func TestUpdateSettings_NoEncryptionKey_ReturnsClearErrorNotPanic(t *testing.T) {
	t.Parallel()

	// No storage secret and no JWT secret: NewTokenEncryptorOrDefault fails to
	// build a real TokenEncryptor and degrades to its null-object default,
	// exactly as cmd/server/main.go does at startup when both env vars are
	// unset.
	enc := services.NewTokenEncryptorOrDefault("", "")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"password": "a-restic-password",
	})
	w := httptest.NewRecorder()

	require.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	}, "PUT /settings/backup must not panic when no encryption key is configured")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrEncryptionUnavailable, body["code"])
	assert.Contains(t, body["message"], "STORAGE_KEY")
}

// TestUpdateSettings_DatabaseConstructedNoEncryptor_Returns422NotInternalError
// (agent-os-2fb) covers the sibling path to the test above: a DB built via
// database.New/NewWithMigrations rather than through
// services.NewTokenEncryptorOrDefault. Both constructors install a
// package-local noEncryptor whose Encrypt/Decrypt return that package's own
// ErrEncryptionUnavailable sentinel. respondIfEncryptionUnavailable only
// checked errors.Is against services.ErrEncryptionUnavailable, so — before
// the two sentinels were unified — this path fell through to a generic 500
// INTERNAL_ERROR instead of the actionable 422 ENCRYPTION_KEY_MISSING.
func TestUpdateSettings_DatabaseConstructedNoEncryptor_Returns422NotInternalError(t *testing.T) {
	t.Parallel()

	// database.NewWithMigrations (no encryptor argument) mirrors any caller
	// that builds a DB directly from the database package rather than via
	// services.NewTokenEncryptorOrDefault.
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"password": "a-restic-password",
	})
	w := httptest.NewRecorder()

	require.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	}, "PUT /settings/backup must not panic when the DB has no encryptor")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrEncryptionUnavailable, body["code"])
	assert.Contains(t, body["message"], "STORAGE_KEY")

	// The password must never have been persisted at all (GetSetting errors
	// with "no rows" because SetSetting never got past the encryption
	// failure to INSERT anything).
	stored, err := db.GetSetting("restic_password")
	assert.Error(t, err)
	assert.Empty(t, stored)
}

func TestUpdateSettings_ScheduleChangeTriggersStopStart(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	sched := &handlerFakeScheduler{}
	svc.SetScheduler(sched)

	interval := 60
	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"scheduleIntervalMinutes": interval,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Stop must have been called.
	assert.True(t, sched.stopped, "StopScheduler must be called when interval changes")
	assert.True(t, sched.started, "StartScheduler must be called when new interval > 0")
}

func TestUpdateSettings_ScheduleZeroOnlyStops(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	sched := &handlerFakeScheduler{}
	svc.SetScheduler(sched)

	interval := 0
	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"scheduleIntervalMinutes": interval,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	assert.True(t, sched.stopped, "StopScheduler must be called")
	assert.False(t, sched.started, "StartScheduler must NOT be called when interval is 0")
}

// handlerFakeScheduler is a BackupScheduler stub for handler tests.
type handlerFakeScheduler struct {
	started bool
	stopped bool
	// scheduled records the DailySchedule StartScheduled was called with, so a
	// test can tell "started in interval mode" from "started in scheduled
	// mode". nil means StartScheduled was never called.
	scheduled *services.DailySchedule
}

func (s *handlerFakeScheduler) Start(_ time.Duration) {
	s.started = true
}

func (s *handlerFakeScheduler) StartScheduled(sched services.DailySchedule) {
	s.started = true
	s.scheduled = &sched
}

func (s *handlerFakeScheduler) Stop() {
	s.stopped = true
}

// ─────────────────────────────────────────────
// Policies
// ─────────────────────────────────────────────

func TestListPolicies_EmptyList(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/policies", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	policies, ok := body["policies"].([]interface{})
	require.True(t, ok, "policies must be an array")
	assert.Empty(t, policies)
}

// TestPreviewSnapshot_RejectsMalformedID is the regression test for M5: the
// snapshot ID from the URL must be validated before it reaches `restic ls`, so
// a flag-like or path-like value cannot be interpreted as a restic flag.
func TestPreviewSnapshot_RejectsMalformedID(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	for _, badID := range []string{"--no-lock", "deadbeefZZ", "nothex", "abc"} {
		req := jsonReq(t, http.MethodGet, "/api/backups/snapshots/"+badID+"/preview", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code, "id %q must be rejected", badID)
		body := decodeBody(t, w)
		assert.Equal(t, models.ErrValidation, body["code"])
	}
}

func TestUpsertPolicy_StackNotFound_Returns404(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPut, "/api/backups/policies/stack/nonexistent-stack", map[string]interface{}{
		"enabled":    true,
		"stopPolicy": "stop",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrStackNotFound, body["code"])
}

func TestUpsertPolicy_Success(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "myapp")
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPut, "/api/backups/policies/stack/myapp", map[string]interface{}{
		"enabled":    true,
		"stopPolicy": "hot",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "hot", body["stopPolicy"])
	assert.Equal(t, true, body["enabled"])
}

func TestDeletePolicy_Returns204(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "myapp")
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	// First create a policy.
	req := jsonReq(t, http.MethodPut, "/api/backups/policies/stack/myapp", map[string]interface{}{
		"enabled":    true,
		"stopPolicy": "stop",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Now delete it.
	req2 := httptest.NewRequest(http.MethodDelete, "/api/backups/policies/stack/myapp", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNoContent, w2.Code)
}

// ─────────────────────────────────────────────
// Status & history
// ─────────────────────────────────────────────

func TestGetStatus_Shape(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, true)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	for _, key := range []string{
		"resticAvailable", "rcloneAvailable", "repositoryInitialized",
		"enabledStackCount", "lastRun", "schedulerRunning",
	} {
		_, ok := body[key]
		assert.True(t, ok, "status response must contain key %q", key)
	}
}

func TestGetStatus_NextRunAtNilWhenSchedulerOff(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	// Scheduler is not started → NextRunAt must be nil → JSON null.
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	// nextRunAt must be present in the response and null when scheduler is off.
	_, hasKey := body["nextRunAt"]
	assert.True(t, hasKey, "nextRunAt must be present in status response")
	assert.Nil(t, body["nextRunAt"], "nextRunAt must be null when scheduler is not running")
}

func TestGetStatus_RepoSizeBytesNilWhenRepoUnreachable(t *testing.T) {
	t.Parallel()

	// With no real restic binary and no repository, repoStatus.RepoReachable
	// will be false, so repoSizeBytes must be null.
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	_, hasKey := body["repoSizeBytes"]
	assert.True(t, hasKey, "repoSizeBytes must be present in status response")
	assert.Nil(t, body["repoSizeBytes"], "repoSizeBytes must be null when repo is not reachable")
}

func TestGetHistory_Shape(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)

	runs, ok := body["runs"].([]interface{})
	require.True(t, ok, "runs must be an array")
	assert.Empty(t, runs)
}

// ─────────────────────────────────────────────
// Runs detail
// ─────────────────────────────────────────────

func TestGetRunDetail_NotFound(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/backups/runs/unknown-run-id", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrNotFound, body["code"])
}

// ─────────────────────────────────────────────
// Run kickoff — POST /backups/run
// ─────────────────────────────────────────────

func TestRunBackup_Kickoff_Returns202WithRunIdAndWsUrl(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false) // restic present → available
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/run", map[string]interface{}{
		"stackIds": []string{},
		"dryRun":   false,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID, ok := body["runId"].(string)
	require.True(t, ok && runID != "", "runId must be a non-empty string")

	wsURL, ok := body["wsUrl"].(string)
	require.True(t, ok, "wsUrl must be present")
	assert.True(t, strings.HasPrefix(wsURL, "/ws/backups/run/"), "wsUrl must start with /ws/backups/run/")
}

func TestRunBackup_Kickoff_PersistsDurableRunRecord(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/run", map[string]interface{}{
		"stackIds": []string{"stack-a"},
		"dryRun":   true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)
	runID := body["runId"].(string)
	require.NotEmpty(t, runID)

	// The durable run record must exist in the DB immediately after the 202
	// response (before any WS connection). This is the Finding #7 guard.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "BackupRun row must be persisted at kickoff time")
	assert.Equal(t, "backup", run.Kind)
	assert.Equal(t, "manual", run.Trigger)
	// Status is "running" at kickoff (the goroutine updates it on completion).
	assert.NotEmpty(t, run.Status)
}

func TestRunBackup_EngineUnavailable_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, false) // neither binary present → unavailable
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/run", map[string]interface{}{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "BACKUP_UNAVAILABLE", body["code"])
}

func TestRunBackup_Busy_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	svc.ForceSetBusy(true) // mark busy
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/run", map[string]interface{}{})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	svc.ForceSetBusy(false)

	require.Equal(t, http.StatusConflict, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "BACKUP_BUSY", body["code"])
}

// ─────────────────────────────────────────────
// Sync kickoff
// ─────────────────────────────────────────────

func TestRunSync_Kickoff_Returns202(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/backups/sync", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID, ok := body["runId"].(string)
	require.True(t, ok && runID != "")
	wsURL := body["wsUrl"].(string)
	assert.True(t, strings.HasPrefix(wsURL, "/ws/backups/sync/"))

	// Durable record must exist at kickoff time.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "sync run record must be persisted at kickoff")
	assert.Equal(t, "sync", run.Kind)
}

// ─────────────────────────────────────────────
// Restore kickoff
// ─────────────────────────────────────────────

func TestRunRestore_Kickoff_Returns202(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "myapp")
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
		"stackId":    "myapp",
		"snapshotId": "abc123",
		"target":     "/opt/stacks/myapp",
		"confirm":    true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID, ok := body["runId"].(string)
	require.True(t, ok && runID != "")
	wsURL := body["wsUrl"].(string)
	assert.True(t, strings.HasPrefix(wsURL, "/ws/backups/restore/"))

	// Durable record must exist at kickoff time (before any WS connection).
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "restore run record must be persisted at kickoff")
	assert.Equal(t, "restore", run.Kind)
	// Status is "running" at kickoff, but the execRestore goroutine races this
	// read and finalises the row to "failed" the moment snapshot validation
	// rejects the unconfigured restic password — so the exact value here is
	// scheduler-dependent, not an invariant. Asserting it flaked in CI twice.
	// Matches TestRunBackup_Kickoff_PersistsDurableRunRecord. See agent-os-icp.
	assert.NotEmpty(t, run.Status)
}

func TestRunRestore_StackNotFound_Returns404(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
		"stackId":    "no-such-stack",
		"snapshotId": "abc123",
		"confirm":    true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrStackNotFound, body["code"])
}

func TestRunRestore_NoConfirm_Returns400(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "myapp")
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	// confirm omitted (defaults false) — restore is destructive and must be gated.
	req := jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
		"stackId":    "myapp",
		"snapshotId": "abc123",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "CONFIRMATION_REQUIRED", body["code"])
}

func TestRunRestore_MissingFields_Returns400(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	// Missing snapshotId.
	req := jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
		"stackId": "myapp",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrValidation, body["code"])
}

// ─────────────────────────────────────────────
// DR-Restore kickoff
// ─────────────────────────────────────────────

func TestRunDRRestore_NoConfirm_Returns400(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, true)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/dr-restore", map[string]interface{}{
		"confirm": false,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "CONFIRMATION_REQUIRED", body["code"])
}

func TestRunDRRestore_Kickoff_Returns202(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, true) // rclone present (dr-restore checks rclone)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	// localRepoPath is a removed/ignored field: the destination is derived
	// server-side (C1). Sending it must not be honoured or cause an error.
	req := jsonReq(t, http.MethodPost, "/api/backups/dr-restore", map[string]interface{}{
		"confirm":       true,
		"localRepoPath": "/tmp/restored",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID, ok := body["runId"].(string)
	require.True(t, ok && runID != "")
	wsURL := body["wsUrl"].(string)
	assert.True(t, strings.HasPrefix(wsURL, "/ws/backups/dr-restore/"))

	// Durable record must exist at kickoff time.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "dr_restore run record must be persisted at kickoff")
	assert.Equal(t, "dr_restore", run.Kind)
}

func TestRunDRRestore_Busy_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, true)
	svc.ForceSetBusy(true)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/dr-restore", map[string]interface{}{
		"confirm": true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	svc.ForceSetBusy(false)

	require.Equal(t, http.StatusConflict, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "BACKUP_BUSY", body["code"])
}

// ─────────────────────────────────────────────
// Prune kickoff
// ─────────────────────────────────────────────

func TestRunPrune_NoConfirmNoDryRun_Returns400(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/prune", map[string]interface{}{
		"confirm": false,
		"dryRun":  false,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, "CONFIRMATION_REQUIRED", body["code"])
}

func TestRunPrune_WithConfirm_Returns202(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/prune", map[string]interface{}{
		"confirm": true,
		"dryRun":  false,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID, ok := body["runId"].(string)
	require.True(t, ok && runID != "")

	// Durable record must exist at kickoff time.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "prune run record must be persisted at kickoff")
	assert.Equal(t, "prune", run.Kind)
}

func TestRunPrune_WithDryRunOnly_Returns202(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/prune", map[string]interface{}{
		"confirm": false,
		"dryRun":  true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID := body["runId"].(string)
	require.NotEmpty(t, runID)

	// Durable record must exist at kickoff time.
	run, err := db.GetBackupRunByID(runID)
	require.NoError(t, err, "prune (dry-run) run record must be persisted at kickoff")
	assert.Equal(t, "prune", run.Kind)
}

func TestRunPrune_EngineUnavailable_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, false) // no binaries
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/prune", map[string]interface{}{
		"confirm": true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
}

// ─────────────────────────────────────────────
// BackupRunnerRegistry — durable registry tests
// ─────────────────────────────────────────────

// TestRegistry_AttachUnknownRunID verifies that Attach returns an error for a
// completely unknown runID (not in registry, not in DB).
func TestRegistry_AttachUnknownRunID(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	_, err := reg.Attach("completely-unknown-run-id", nil)
	require.Error(t, err, "Attach must return an error for an unknown runID")
}

// TestRegistry_LaunchBackup_PersistsDurableRecord verifies that LaunchBackup
// persists a BackupRun row synchronously before the goroutine starts, so the
// run is durable whether or not any WS client ever connects.
func TestRegistry_LaunchBackup_PersistsDurableRecord(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup(nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	// The DB record must exist before any WS connects.
	run, dbErr := db.GetBackupRunByID(runID)
	require.NoError(t, dbErr, "BackupRun row must exist at kickoff time")
	assert.Equal(t, "backup", run.Kind)
	assert.Equal(t, "manual", run.Trigger)
}

// TestRegistry_AttachFinishedRun verifies that Attach on a finished run returns
// Done=true with the terminal outcome — used by the WS handler to replay the
// final status to late-joining clients.
func TestRegistry_AttachFinishedRun(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup(nil, false)
	require.NoError(t, err)

	// Wait for the goroutine to finish (it will fail — no real restic).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ar, aErr := reg.Attach(runID, nil)
		if aErr != nil {
			t.Fatalf("Attach error: %v", aErr)
		}
		if ar.Done {
			// Goroutine finished; outcome must be set.
			assert.NotEmpty(t, ar.Outcome)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("durable run never reached Done=true within 5 s")
}

// panicRcloneRunner is a fake CommandRunner that panics on Run.
// It is used to verify that recoverExec catches a panic inside an exec goroutine
// and finalises the run as "failed" rather than crashing the process.
type panicRcloneRunner struct{}

func (r *panicRcloneRunner) Run(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
	_ chan<- services.StreamLine,
) error {
	panic("injected panic for recoverExec test")
}

func (r *panicRcloneRunner) Output(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
) ([]byte, error) {
	return []byte(`{}`), nil
}

// TestRegistry_PanicInExec_RunTerminatesAsFailed verifies that a panic inside
// an exec goroutine (e.g. inside a service method) is caught by recoverExec,
// the DB record reaches status="failed", and Attach reports Done with
// outcome="failed" — proving the process does not crash and the run does not
// remain stuck at "running".
//
// Load-bearing: if defer reg.recoverExec(dr) were removed from execSync, the
// goroutine panic would propagate out of the goroutine and crash the test
// binary (Go panics that escape a goroutine are fatal). The test would never
// reach the assertions.
func TestRegistry_PanicInExec_RunTerminatesAsFailed(t *testing.T) {
	// Not parallel — injects a panicking runner; must not interfere with other tests.

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, true) // rcloneBin set → sync is "available"

	// Inject a rclone manager factory whose runner panics on every Run call.
	svc.SetRcloneMgrFactory(func(bc services.BackupConfig) *services.RcloneManager {
		return services.NewRcloneManagerForTest(bc, &panicRcloneRunner{}, slog.Default())
	})

	// Provide the minimum rclone config so RunSync does not return early with
	// ErrBackupUnavailable or "remote not configured" before reaching the runner.
	require.NoError(t, db.SetSetting("rclone_remote", "fakeprovider"))
	require.NoError(t, db.SetSetting("restic_repository", "/tmp/test-repo"))

	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchSync()
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	// The DB record must exist synchronously after LaunchSync.
	initialRun, dbErr := db.GetBackupRunByID(runID)
	require.NoError(t, dbErr)
	assert.Equal(t, "sync", initialRun.Kind)

	// Wait for the exec goroutine to exit.  recoverExec must:
	//   (a) catch the panic without crashing the binary,
	//   (b) write outcome="failed" to the durableRun, and
	//   (c) update the DB record to status="failed".
	deadline := time.Now().Add(5 * time.Second)
	var finalAR *services.AttachResult
	for time.Now().Before(deadline) {
		ar, aErr := reg.Attach(runID, nil)
		require.NoError(t, aErr)
		if ar.Done {
			finalAR = ar
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.NotNil(t, finalAR, "run must reach Done=true within 5 s after panic")

	assert.Equal(t, "failed", finalAR.Outcome,
		"outcome must be 'failed' when the exec goroutine panics")

	// Confirm the DB record was also updated.
	dbRun, dbErr := db.GetBackupRunByID(runID)
	require.NoError(t, dbErr)
	assert.Equal(t, "failed", dbRun.Status,
		"DB status must be 'failed', not 'running', after a panic in the exec goroutine")
	assert.NotNil(t, dbRun.FinishedAt,
		"FinishedAt must be set after recoverExec finalises the run")
}

// ─────────────────────────────────────────────
// Repository path resolution (agent-os-9au)
// ─────────────────────────────────────────────

// recordingResticRunner is a fake CommandRunner that records every invocation
// with the environment it was given, and fails `restic snapshots` so callers
// treat the repository as not yet initialised and proceed to `restic init`.
type recordingResticRunner struct {
	mu    sync.Mutex
	calls []recordedResticCall

	// failRepoProbe makes `restic snapshots --quiet` fail, which is how
	// BackupService.CheckRepository decides a repository is not reachable.
	// Set it when the code under test should proceed to `restic init`; leave it
	// false when the repository must look reachable (e.g. snapshot listing,
	// which returns an empty list early if the probe fails).
	failRepoProbe bool
}

type recordedResticCall struct {
	args []string
	env  []string
}

func (r *recordingResticRunner) record(args, env []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedResticCall{args: args, env: env})
}

// repoFor returns the RESTIC_REPOSITORY the runner was given for the first
// invocation whose first argument is subcommand and whose argument list
// contains every string in mustContain. ok is false if no such invocation was
// recorded.
//
// mustContain matters: BackupService.CheckRepository also shells out to
// `restic snapshots --quiet`, and it already resolves its config correctly. A
// test that matched on the subcommand alone would observe that call and pass
// regardless of the bug. Matching on `--json` pins the listing path instead.
func (r *recordingResticRunner) repoFor(subcommand string, mustContain ...string) (repo string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if len(c.args) == 0 || c.args[0] != subcommand {
			continue
		}
		if !argsContainAll(c.args, mustContain) {
			continue
		}
		for _, e := range c.env {
			if strings.HasPrefix(e, "RESTIC_REPOSITORY=") {
				return strings.TrimPrefix(e, "RESTIC_REPOSITORY="), true
			}
		}
		return "", true
	}
	return "", false
}

// argsContainAll reports whether args contains every string in want.
func argsContainAll(args, want []string) bool {
	for _, w := range want {
		found := false
		for _, a := range args {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (r *recordingResticRunner) Run(
	_ context.Context,
	_ string,
	args []string,
	env []string,
	_ chan<- services.StreamLine,
) error {
	r.record(args, env)
	// BackupService.CheckRepository probes with `restic snapshots --quiet`.
	// `snapshots --json` (the listing path) is a different invocation and must
	// never be failed by this probe.
	if r.failRepoProbe && argsContainAll(args, []string{"snapshots", "--quiet"}) {
		return errors.New("repository does not exist")
	}
	return nil
}

func (r *recordingResticRunner) Output(
	_ context.Context,
	_ string,
	args []string,
	env []string,
) ([]byte, error) {
	r.record(args, env)
	return []byte(`[]`), nil
}

// TestRepoInit_InitialisesRepositoryUnderDataDir pins the fix for agent-os-9au.
//
// repoInit used to resolve its BackupConfig with services.ResolveBackupConfig(db),
// which passed an EMPTY &config.Config{}. With no DataDir the default repository
// became filepath.Join("", "restic-repo") — the RELATIVE path "restic-repo",
// resolved against the server's working directory. Every other code path used
// the service's config and correctly produced <DataDir>/restic-repo, so init
// created a repository somewhere the backups never looked, reported success, and
// every subsequent backup failed with "repository does not exist".
//
// Observed on a real container before the fix: init logged path=restic-repo and
// created /app/restic-repo, while GET /settings/backup reported
// /app/data/restic-repo and repositoryInitialized=false.
//
// This test fails against the old code for the right reason: repoInit built its
// own ResticManager with services.NewResticManager, bypassing the service
// factory entirely, so the injected runner never saw the `init` call at all.
func TestRepoInit_InitialisesRepositoryUnderDataDir(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_password", "test-repo-password"))

	svc := buildBackupSvc(t, db, true, false)
	// The repository must look uninitialised so repoInit proceeds to `init`.
	runner := &recordingResticRunner{failRepoProbe: true}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, slog.Default())
	})

	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/repo/init", map[string]interface{}{}))

	require.Equal(t, http.StatusOK, w.Code, "repo init should succeed against the injected runner")

	repo, ok := runner.repoFor("init")
	require.True(t, ok,
		"repoInit must build its ResticManager through the service factory; "+
			"constructing one directly bypasses both the injected runner and the live config")

	wantRepo := filepath.Join(svc.Config().DataDir, "restic-repo")
	assert.Equal(t, wantRepo, repo,
		"restic init must target <DataDir>/restic-repo, not a path relative to the process working directory")
	assert.True(t, filepath.IsAbs(repo), "the resolved repository must be an absolute path")
}

// TestSnapshotListing_ResolvesRepositoryUnderDataDir covers the same defect on
// the snapshot-listing path, which shared the DataDir-less resolver.
func TestSnapshotListing_ResolvesRepositoryUnderDataDir(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_password", "test-repo-password"))

	svc := buildBackupSvc(t, db, true, false)
	runner := &recordingResticRunner{}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, slog.Default())
	})

	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine this test kicked off has
	// finished (including its DB write), so it must run BEFORE db.Close() and the
	// t.TempDir() cleanup registered above — t.Cleanup runs LIFO, and this is
	// registered last, so it runs first. See agent-os-80n.
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

	repo, ok := runner.repoFor("snapshots", "--json")
	require.True(t, ok, "snapshot listing must build its ResticManager through the service factory")

	assert.Equal(t, filepath.Join(svc.Config().DataDir, "restic-repo"), repo,
		"restic snapshots must target <DataDir>/restic-repo")
}

// TestBackupHandler_StopWithTimeout_DelegatesAndIsIdempotent is the
// agent-os-7a5 regression test for wiring BackupHandler.Stop into main.go's
// shutdown path: main.go calls StopWithTimeout, so the handler must expose
// it and forward to the registry, and — because main.go's call is a second
// real caller alongside every test's t.Cleanup(h.Stop) — mixing StopWithTimeout
// and Stop on the same handler must not panic.
func TestBackupHandler_StopWithTimeout_DelegatesAndIsIdempotent(t *testing.T) {
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())

	// No runs in flight, so this must complete well within the bound.
	completed := h.StopWithTimeout(2 * time.Second)
	assert.True(t, completed, "StopWithTimeout must report completion when nothing is in flight")

	// A second call (StopWithTimeout again, and Stop) must not panic — this is
	// the idempotence guarantee agent-os-7a5 adds via BackupRunnerRegistry's
	// stopped flag (set once, under mu, by beginStop).
	assert.True(t, h.StopWithTimeout(2*time.Second))
	h.Stop()
}

// TestRunBackup_AfterShutdownBegun_Returns503 is the handler-level half of
// agent-os-7a5's WaitGroup-safety fix: a launch request arriving after the
// registry has committed to shutting down (h.registry.Stop/StopWithTimeout
// already called) must surface as 503 Service Unavailable — an availability
// condition a client can retry post-restart — not the generic 500
// internalError previously returned for every LaunchX failure.
func TestRunBackup_AfterShutdownBegun_Returns503(t *testing.T) {
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())

	h.registry.Stop() // commit to shutdown; nothing in flight, returns immediately

	r := newBackupRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/run", map[string]interface{}{
		"stackIds": []string{},
		"dryRun":   true,
	}))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "SERVER_SHUTTING_DOWN", body["code"])
}

// ─────────────────────────────────────────────
// Fixed-clock-time schedule settings — agent-os-mtbo.3
// ─────────────────────────────────────────────

// TestGetSettings_ScheduleDefaults verifies the new response fields exist with
// their documented defaults on a fresh install, and in particular that
// scheduleDays is an ARRAY and never JSON null — the UI iterates it directly.
func TestGetSettings_ScheduleDefaults(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := decodeBody(t, w)
	assert.Equal(t, "interval", body["scheduleMode"], "default mode must preserve today's behaviour")
	assert.Equal(t, "02:00", body["scheduleTime"])

	days, ok := body["scheduleDays"].([]interface{})
	require.True(t, ok, "scheduleDays must be a JSON array, got %#v", body["scheduleDays"])
	assert.Len(t, days, 7, "the default schedule is every day")

	assert.NotEmpty(t, body["serverTimezone"], "the UI renders the zone name beside the time field")
	offset, ok := body["serverTimeOffset"].(string)
	require.True(t, ok)
	assert.Regexp(t, `^[+-]\d{2}:\d{2}$`, offset)
}

// TestGetSettings_ScheduleDaysNeverNull is the negative half of the array
// guarantee: even an unparseable stored value must serialise as [], not null.
func TestGetSettings_ScheduleDaysNeverNull(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_days", "not,a,day"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Contains(t, w.Body.String(), `"scheduleDays":[]`,
		"scheduleDays must serialise as an empty array, never null")
}

// TestUpdateSettings_ScheduleFieldsRoundTrip PUTs the three new fields through
// the real handler and GETs them back. gin silently accepts unknown JSON
// fields, so a struct-tag typo would otherwise ship green with the value
// simply never arriving.
func TestUpdateSettings_ScheduleFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)
	svc.SetScheduler(&handlerFakeScheduler{})

	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"scheduleMode": "scheduled",
		"scheduleTime": "23:15",
		// Deliberately unsorted and duplicated: the stored form is normalised.
		"scheduleDays": []int{5, 1, 1},
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// The PUT response is getSettings, so it already carries the new values.
	body := decodeBody(t, w)
	assert.Equal(t, "scheduled", body["scheduleMode"])
	assert.Equal(t, "23:15", body["scheduleTime"])
	assert.Equal(t, []interface{}{float64(1), float64(5)}, body["scheduleDays"])

	// And a fresh GET must agree, proving the values were persisted.
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	getBody := decodeBody(t, getW)
	assert.Equal(t, "scheduled", getBody["scheduleMode"])
	assert.Equal(t, "23:15", getBody["scheduleTime"])
	assert.Equal(t, []interface{}{float64(1), float64(5)}, getBody["scheduleDays"])

	stored, err := db.GetSetting("backup_schedule_days")
	require.NoError(t, err)
	assert.Equal(t, "1,5", stored, "weekdays are stored sorted and deduped")
}

// TestUpdateSettings_AbsentScheduleFieldsAreUnchanged: the UI sends a field
// only when it differs, so absent must mean unchanged.
func TestUpdateSettings_AbsentScheduleFieldsAreUnchanged(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("backup_schedule_mode", "scheduled"))
	require.NoError(t, db.SetSetting("backup_schedule_time", "04:05"))
	require.NoError(t, db.SetSetting("backup_schedule_days", "2,4"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)
	svc.SetScheduler(&handlerFakeScheduler{})

	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"keepDaily": 9,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := decodeBody(t, w)
	assert.Equal(t, "scheduled", body["scheduleMode"])
	assert.Equal(t, "04:05", body["scheduleTime"])
	assert.Equal(t, []interface{}{float64(2), float64(4)}, body["scheduleDays"])
}

// TestUpdateSettings_InvalidScheduleValuesReturn400 covers every rejecting
// case; TestUpdateSettings_ScheduleFieldsRoundTrip above is the accepting
// control on the same endpoint.
func TestUpdateSettings_InvalidScheduleValuesReturn400(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"unknown mode", map[string]interface{}{"scheduleMode": "hourly"}},
		{"empty mode", map[string]interface{}{"scheduleMode": ""}},
		{"time out of range", map[string]interface{}{"scheduleTime": "25:00"}},
		{"time minute out of range", map[string]interface{}{"scheduleTime": "02:60"}},
		{"time not zero padded", map[string]interface{}{"scheduleTime": "2:00"}},
		{"time with seconds", map[string]interface{}{"scheduleTime": "02:00:00"}},
		{"time garbage", map[string]interface{}{"scheduleTime": "later"}},
		{"weekday above range", map[string]interface{}{"scheduleDays": []int{7}}},
		{"weekday below range", map[string]interface{}{"scheduleDays": []int{-1}}},
		{"no weekdays selected", map[string]interface{}{"scheduleDays": []int{}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newBackupHandlerDB(t)
			svc := buildBackupSvc(t, db, true, false)
			h := NewBackupHandler(svc, db, slog.Default())
			t.Cleanup(h.Stop)
			r := newBackupRouter(h)

			sched := &handlerFakeScheduler{}
			svc.SetScheduler(sched)

			req := jsonReq(t, http.MethodPut, "/api/settings/backup", tc.body)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrValidation, body["code"])

			// Validation happens before any write, so nothing may be persisted
			// and the running scheduler must not be disturbed.
			for _, key := range []string{"backup_schedule_mode", "backup_schedule_time", "backup_schedule_days"} {
				stored, err := db.GetSetting(key)
				assert.Empty(t, stored, "%s must not be written by a rejected request (err=%v)", key, err)
			}
			assert.False(t, sched.stopped, "a rejected request must not stop the scheduler")
		})
	}
}

// TestUpdateSettings_ModeChangeRestartsScheduler covers finding C: the restart
// used to be gated on the interval field alone, so switching to scheduled mode
// left the old ticker running until the next process restart.
func TestUpdateSettings_ModeChangeRestartsScheduler(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	// No interval row at all: scheduled mode must not need one.
	require.NoError(t, db.SetSetting("backup_schedule_time", "01:30"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	sched := &handlerFakeScheduler{}
	svc.SetScheduler(sched)

	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"scheduleMode": "scheduled",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.True(t, sched.stopped, "a mode change must stop the running scheduler")
	require.NotNil(t, sched.scheduled, "a mode change to scheduled must re-arm on the scheduled path")
	assert.Equal(t, 1, sched.scheduled.Hour)
	assert.Equal(t, 30, sched.scheduled.Minute)
}

// TestUpdateSettings_TimeAndDaysChangesRestartScheduler covers the other two
// fields of finding C, each on its own.
func TestUpdateSettings_TimeAndDaysChangesRestartScheduler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"time only", map[string]interface{}{"scheduleTime": "06:00"}},
		{"days only", map[string]interface{}{"scheduleDays": []int{0, 6}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newBackupHandlerDB(t)
			require.NoError(t, db.SetSetting("backup_schedule_mode", "scheduled"))

			svc := buildBackupSvc(t, db, true, false)
			h := NewBackupHandler(svc, db, slog.Default())
			t.Cleanup(h.Stop)
			r := newBackupRouter(h)

			sched := &handlerFakeScheduler{}
			svc.SetScheduler(sched)

			req := jsonReq(t, http.MethodPut, "/api/settings/backup", tc.body)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			assert.True(t, sched.stopped, "changing %s must stop the running scheduler", tc.name)
			assert.NotNil(t, sched.scheduled, "changing %s must re-arm the scheduler", tc.name)
		})
	}
}

// TestUpdateSettings_NonScheduleChangeDoesNotRestartScheduler is the control
// for the two tests above: widening the restart trigger must not make every
// unrelated settings save bounce the scheduler.
func TestUpdateSettings_NonScheduleChangeDoesNotRestartScheduler(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	sched := &handlerFakeScheduler{}
	svc.SetScheduler(sched)

	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{
		"keepDaily":    21,
		"rclonePath":   "somewhere/else",
		"rcloneRemote": "other",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.False(t, sched.stopped, "a non-schedule change must leave the scheduler alone")
	assert.False(t, sched.started)
}
