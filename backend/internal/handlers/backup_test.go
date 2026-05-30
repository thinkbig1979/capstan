package handlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// ─────────────────────────────────────────────
// Test infrastructure
// ─────────────────────────────────────────────

func newBackupHandlerDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
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

func (n *noopDocker) Stop(stack models.Stack) (*models.CommandResult, error) {
	return &models.CommandResult{ExitCode: 0}, nil
}
func (n *noopDocker) Start(stack models.Stack) (*models.CommandResult, error) {
	return &models.CommandResult{ExitCode: 0}, nil
}
func (n *noopDocker) Status(stack models.Stack) (string, []models.Container, error) {
	return "stopped", nil, nil
}

// newBackupRouter wires a BackupHandler onto a gin.Engine with all REST routes
// registered. It mirrors the pattern used in stacks_test.go.
func newBackupRouter(h *BackupHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
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

// ─────────────────────────────────────────────
// updateSettings
// ─────────────────────────────────────────────

func TestUpdateSettings_WritesSettings(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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

func TestUpdateSettings_ScheduleChangeTriggersStopStart(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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
}

func (s *handlerFakeScheduler) Start(_ time.Duration) {
	s.started = true
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

func TestUpsertPolicy_StackNotFound_Returns404(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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

func TestGetHistory_Shape(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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

func TestRunBackup_Kickoff_StashesOp(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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

	// Pop the op from the registry and assert it has the right kind.
	op := popOp(runID)
	require.NotNil(t, op, "op must be stashed under runId")
	assert.Equal(t, opKindRun, op.kind)
	assert.Equal(t, []string{"stack-a"}, op.stackIDs)
	assert.True(t, op.dryRun)
}

func TestRunBackup_EngineUnavailable_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, false) // neither binary present → unavailable
	h := NewBackupHandler(svc, db, slog.Default())
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

	op := popOp(runID)
	require.NotNil(t, op)
	assert.Equal(t, opKindSync, op.kind)
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
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
		"stackId":    "myapp",
		"snapshotId": "abc123",
		"target":     "/opt/stacks/myapp",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	body := decodeBody(t, w)

	runID, ok := body["runId"].(string)
	require.True(t, ok && runID != "")
	wsURL := body["wsUrl"].(string)
	assert.True(t, strings.HasPrefix(wsURL, "/ws/backups/restore/"))

	op := popOp(runID)
	require.NotNil(t, op)
	assert.Equal(t, opKindRestore, op.kind)
	assert.Equal(t, "myapp", op.stackID)
	assert.Equal(t, "abc123", op.snapshotID)
	assert.Equal(t, "/opt/stacks/myapp", op.target)
}

func TestRunRestore_StackNotFound_Returns404(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
		"stackId":    "no-such-stack",
		"snapshotId": "abc123",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrStackNotFound, body["code"])
}

func TestRunRestore_MissingFields_Returns400(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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
	r := newBackupRouter(h)

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

	op := popOp(runID)
	require.NotNil(t, op)
	assert.Equal(t, opKindDRRestore, op.kind)
	assert.Equal(t, "/tmp/restored", op.localRepoPath)
}

func TestRunDRRestore_Busy_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, true)
	svc.ForceSetBusy(true)
	h := NewBackupHandler(svc, db, slog.Default())
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

	op := popOp(runID)
	require.NotNil(t, op)
	assert.Equal(t, opKindPrune, op.kind)
	assert.False(t, op.dryRun)
}

func TestRunPrune_WithDryRunOnly_Returns202(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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
	op := popOp(runID)
	require.NotNil(t, op)
	assert.Equal(t, opKindPrune, op.kind)
	assert.True(t, op.dryRun)
}

func TestRunPrune_EngineUnavailable_Returns409(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, false) // no binaries
	h := NewBackupHandler(svc, db, slog.Default())
	r := newBackupRouter(h)

	req := jsonReq(t, http.MethodPost, "/api/backups/prune", map[string]interface{}{
		"confirm": true,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
}

// ─────────────────────────────────────────────
// WS registry: stashOp / popOp round-trips
// ─────────────────────────────────────────────

func TestRegistry_StashPopRoundTrip(t *testing.T) {
	t.Parallel()

	op := &pendingOp{
		kind:     opKindRun,
		stackIDs: []string{"stack-x"},
		dryRun:   true,
	}
	stashOp("test-id-roundtrip", op)

	got := popOp("test-id-roundtrip")
	require.NotNil(t, got)
	assert.Equal(t, opKindRun, got.kind)
	assert.Equal(t, []string{"stack-x"}, got.stackIDs)
	assert.True(t, got.dryRun)
}

func TestRegistry_PopUnknownRunIdReturnsNil(t *testing.T) {
	t.Parallel()

	got := popOp("completely-unknown-run-id-xyz")
	assert.Nil(t, got)
}

func TestRegistry_PopConsumesSoSecondPopReturnsNil(t *testing.T) {
	t.Parallel()

	stashOp("one-shot-id", &pendingOp{kind: opKindSync})

	first := popOp("one-shot-id")
	require.NotNil(t, first)

	second := popOp("one-shot-id")
	assert.Nil(t, second, "popOp must be destructive — second pop must return nil")
}

func TestRegistry_ExpiredEntryReturnsNil(t *testing.T) {
	t.Parallel()

	op := &pendingOp{
		kind: opKindPrune,
	}
	// Bypass stashOp to set an already-expired expiresAt.
	op.expiresAt = time.Now().Add(-1 * time.Hour) // one hour in the past

	pendingOpsMu.Lock()
	pendingOps["expired-id-test"] = op
	pendingOpsMu.Unlock()

	got := popOp("expired-id-test")
	assert.Nil(t, got, "expired op must not be returned by popOp")
}
