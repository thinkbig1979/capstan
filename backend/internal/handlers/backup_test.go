package handlers

import (
	"bytes"
	"context"
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

func TestGetSettings_RepositorySource_DB(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", "/db/repo"))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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

// TestPreviewSnapshot_RejectsMalformedID is the regression test for M5: the
// snapshot ID from the URL must be validated before it reaches `restic ls`, so
// a flag-like or path-like value cannot be interpreted as a restic flag.
func TestPreviewSnapshot_RejectsMalformedID(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
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

func TestGetStatus_NextRunAtNilWhenSchedulerOff(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	// Scheduler is not started → NextRunAt must be nil → JSON null.
	h := NewBackupHandler(svc, db, slog.Default())
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

func TestRunBackup_Kickoff_PersistsDurableRunRecord(t *testing.T) {
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
	assert.Equal(t, "running", run.Status)
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
