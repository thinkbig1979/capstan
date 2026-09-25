package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

	// Registers the "sqlite" driver for the raw database/sql handle in
	// TestBackupSchemaTemplate_CarriesEveryMigration. internal/database imports
	// it too, but that test exists precisely to check the template WITHOUT
	// going through internal/database, so it names its own dependency.
	_ "modernc.org/sqlite"
)

// ─────────────────────────────────────────────
// Test infrastructure
// ─────────────────────────────────────────────

// wantSchemaMigrations is the number of migrations internal/database is
// expected to define, and therefore the schema version every template copy
// must be stamped at.
//
// Deliberately a literal rather than a reference to the database package's own
// count: a test that derives the expected value from the thing it is checking
// cannot fail. Adding migration 20 is meant to fail this test and make someone
// look at the template — that friction is the feature. It did exactly that for
// migration 15 (agent-os-lmbn), migration 16 (agent-os-j1jw), migration 17
// (agent-os-fn7x), migration 18 (agent-os-zt0h) and migration 19
// (agent-os-4i7r): the template is rebuilt from the database package's own
// migration list, so it picked each up automatically and only this literal
// needed moving.
const wantSchemaMigrations = 19

// backupSchemaTemplate returns the bytes of a fully migrated, empty Capstan
// database, built exactly once per test binary.
//
// WHY THIS EXISTS (agent-os-1kio): under -race every SQLite call in the
// process serialises on one process-global allocator mutex —
// modernc.org/libc's allocMu (libc.go:52, taken in Xmalloc/Xfree/Xrealloc).
// This file has 54 t.Parallel tests, and each one running all 15 migrations
// turned the parallel phase into a lock stampede: bursts of tests entering
// migrations separated by multi-second stretches in which nothing completed.
// Running the migrations once and handing every test a byte-for-byte copy
// attacks the allocation VOLUME, which is the half of the problem no
// -parallel cap can touch.
//
// OBSERVED on an idle 8-core box, 20 databases opened under -race: 4.42s via
// migrations, 0.56s via this template (the 0.56s includes building it).
//
// VACUUM INTO rather than copying the file: capstan.db runs in WAL mode, so
// the .db file on its own is not self-contained. See the long note on
// (*database.DB).VacuumInto for why that matters.
var backupSchemaTemplate = sync.OnceValues(buildBackupSchemaTemplate)

func buildBackupSchemaTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "capstan-schema-template-")
	if err != nil {
		return nil, err
	}
	// The bytes are what callers keep; the scratch directory is not needed
	// past this function, so nothing outlives the call and no temp directory
	// leaks for the life of the test binary.
	defer os.RemoveAll(dir)

	// The encryptor never reaches the file. It encrypts individual column
	// values at write time, and a freshly migrated database holds schema plus
	// whatever the migrations seed — no secrets. Each test supplies its own
	// encryptor when it opens its own copy, which is what lets the null-
	// encryptor tests below share this same template.
	src, err := database.NewWithMigrationsAndEncryptor(":memory:", services.NewTokenEncryptorOrDefault("", "test-secret-32-chars-padding-here"))
	if err != nil {
		return nil, err
	}
	defer src.Close()

	dest := filepath.Join(dir, "template.db")
	if err := src.VacuumInto(dest); err != nil {
		return nil, err
	}
	//nolint:gosec // dest is filepath.Join of this function's own os.MkdirTemp
	// directory and a hardcoded constant — it never traces to test input, let
	// alone request input.
	return os.ReadFile(dest)
}

// newMigratedDBDir writes a copy of the migrated template into a fresh
// temporary directory and returns that directory, ready to hand to
// database.New / database.NewWithEncryptor — both of which append
// "/capstan.db" and, unlike their *WithMigrations siblings, do not run the
// migrations.
//
// Note this moves these tests from ":memory:" to a file-backed database, which
// also moves the connection pool from 1 connection to 25 (database.go, the
// dataDir == ":memory:" branch). That is the production configuration rather
// than a weaker one, and it removes a single-connection deadlock hazard rather
// than adding one; concurrent writers are covered by WAL plus the DSN's
// busy_timeout(5000).
func newMigratedDBDir(t *testing.T) string {
	t.Helper()
	tmpl, err := backupSchemaTemplate()
	require.NoError(t, err, "build migrated schema template")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "capstan.db"), tmpl, 0o600))
	return dir
}

// TestBackupSchemaTemplate_CarriesEveryMigration guards the template above. A
// template that silently shipped an older schema would make all 54 tests in
// this file pass against the wrong tables — a far worse failure than the slow
// migrations it replaced — so this asserts the copy a test actually opens is
// stamped at the binary's latest migration.
//
// It opens the copied file with database/sql directly instead of through
// internal/database, so this check uses a different instrument than the code
// it is checking: a bug in that package's own version reporting cannot make
// this test agree with it.
//
// Seen failing: run under a -overlay that truncates internal/database's
// migrations slice to 13 and this fails on its assertion (14 != 13), with the
// package still building — not on a compile error.
func TestBackupSchemaTemplate_CarriesEveryMigration(t *testing.T) {
	t.Parallel()

	raw, err := sql.Open("sqlite", filepath.Join(newMigratedDBDir(t), "capstan.db"))
	require.NoError(t, err)
	t.Cleanup(func() { raw.Close() })

	var maxVersion, applied int
	require.NoError(t, raw.QueryRow(
		"SELECT COALESCE(MAX(version), 0), COUNT(*) FROM schema_migrations",
	).Scan(&maxVersion, &applied))

	assert.Equal(t, wantSchemaMigrations, maxVersion, "schema version stamped on the template copy")
	assert.Equal(t, wantSchemaMigrations, applied, "migrations recorded in the template copy")
}

func newBackupHandlerDB(t *testing.T) *database.DB {
	t.Helper()
	// Use an encryptor-backed DB so sensitive settings (restic_password,
	// git_https_token) can be stored — the DB now refuses to persist secrets in
	// plaintext (L1).
	enc := services.NewTokenEncryptorOrDefault("", "test-secret-32-chars-padding-here")
	db, err := database.NewWithEncryptor(newMigratedDBDir(t), enc)
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
		"resticAvailable", "rcloneAvailable", "repoState", "repoStateMessage",
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
	db, err := database.NewWithEncryptor(newMigratedDBDir(t), enc)
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

	// database.New (no encryptor argument) mirrors any caller that builds a DB
	// directly from the database package rather than via
	// services.NewTokenEncryptorOrDefault: both it and NewWithMigrations
	// install the same package-local noEncryptor, and this path only needs the
	// encryptor, not the migrations, which the template already carries.
	db, err := database.New(newMigratedDBDir(t))
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
		"resticAvailable", "rcloneAvailable", "repoState", "repoStateMessage",
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
		"snapshotId": "abc12345",
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
		"snapshotId": "abc12345",
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
		"snapshotId": "abc12345",
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

// TestRunRestore_InvalidSnapshotID_Returns400 pins agent-os-tyl6: a malformed
// snapshotId is refused before a run is launched. restic never saw it either
// way (RunRestore only restores an id restic listed for the stack), but it used
// to answer 202, take the global backup lock and persist a failed run.
func TestRunRestore_InvalidSnapshotID_Returns400(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"--help", "-h", "abc123", "../etc", "abc12345:/x"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			db := newBackupHandlerDB(t)
			seedHandlerStack(t, db, "myapp")
			svc := buildBackupSvc(t, db, true, false)
			h := NewBackupHandler(svc, db, slog.Default())
			t.Cleanup(h.Stop)
			r := newBackupRouter(h)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/restore", map[string]interface{}{
				"stackId":    "myapp",
				"snapshotId": id,
				"confirm":    true,
			}))

			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrValidation, body["code"])
			assert.Equal(t, "Invalid snapshot ID", body["message"])

			runs, err := db.GetBackupRuns(10)
			require.NoError(t, err)
			assert.Empty(t, runs, "a rejected snapshotId must not persist a run")
		})
	}
	// The accepting side is TestRunRestore_Kickoff_Returns202 ("abc12345").
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

// awaitDurableRun blocks until the durable run identified by runID has
// finished, then re-Attaches and returns its terminal AttachResult. ok is
// false when guard elapsed first, i.e. the run had NOT finished.
//
// It waits on AttachResult.Finished, which is the run's dr.done: a channel
// closed exactly once when the op goroutine's defers run
// (services/backup_runner.go:44-45, closed by `defer close(dr.done)` at
// backup_runner.go:357). Done is therefore a MONOTONE false->true observable,
// never a transient one, so waiting on the condition is sound and guard is a
// hang guard that a passing run never consults. MEASURED, with an overlay
// probe re-Attaching 20 times at 50ms intervals after completion:
// durableSamples=20/20 across 3 runs under -race on an idle box.
//
// dr.outcome is written BEFORE close(dr.done) (defer LIFO at
// backup_runner.go:356-358: recoverExec, then close(dr.done), then wg.Done),
// so the re-Attach below cannot see a closed channel with an unset outcome.
//
// WHY THIS REPLACED A POLL. This used to poll Attach every 50ms under a 5s
// wall-clock budget, which made the goroutine's COMPLETION TIME the assertion:
// the run takes ~73ms idle (OBSERVED, same probe), so a pass depended on a
// bound 68x larger than the work, and a loaded runner turned a correct
// registry red. OBSERVED in CI run 33895843509, "Race detector" job, PR #266:
// `--- FAIL: TestRegistry_AttachFinishedRun (13.63s)` /
// `durable run never reached Done=true within 5 s` (agent-os-25ye). Reproduced
// deterministically here by running the compiled -race test binary under
// `systemd-run --user --scope -p CPUQuota=1%`: it failed at the same line and
// then logged `durable backup finished ... outcome=partial` 68s later, proving
// the run was still legitimately in flight rather than broken.
func awaitDurableRun(
	t *testing.T,
	reg *services.BackupRunnerRegistry,
	runID string,
	guard time.Time,
) (*services.AttachResult, bool) {
	t.Helper()

	ar, err := reg.Attach(runID, nil)
	require.NoError(t, err, "Attach(%s)", runID)
	if ar.Done {
		return ar, true
	}
	require.NotNil(t, ar.Finished,
		"a still-running Attach with a nil clientGone must expose the completion channel")

	select {
	case <-ar.Finished:
	case <-time.After(time.Until(guard)):
		return nil, false
	}

	// Re-Attach for the terminal snapshot: Finished only says the goroutine is
	// done, the outcome comes from the registry (or, after GC eviction, the DB
	// fallback at backup_runner.go:684-714, which likewise reports Done=true).
	ar, err = reg.Attach(runID, nil)
	require.NoError(t, err, "re-Attach(%s) after completion", runID)
	return ar, ar.Done
}

// TestRegistry_AttachFinishedRun verifies that Attach on a finished run returns
// Done=true with the terminal outcome — used by the WS handler to replay the
// final status to late-joining clients.
//
// Its control arm is TestRegistry_AttachUnfinishedRun_IsNotReportedDone below:
// this test would still pass if awaitDurableRun returned ok unconditionally,
// so the pair is what shows the assertion was stabilised rather than removed.
func TestRegistry_AttachFinishedRun(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(reg.Stop)

	runID, err := reg.LaunchBackup(nil, false)
	require.NoError(t, err)

	// The goroutine will fail (no real restic), but it must still reach a
	// terminal state, and Attach must then report that state.
	ar, ok := awaitDurableRun(t, reg, runID, hangGuardDeadline(t))
	require.True(t, ok, "durable run never reached Done=true before the hang guard")
	assert.NotEmpty(t, ar.Outcome)
}

// blockingResticRunner is a CommandRunner whose Run and Output never return
// until release is closed. It is the injection that makes the control arm
// below deterministic rather than timing-dependent.
//
// It deliberately IGNORES the context. ResticManager.CheckRepository wraps its
// ctx with a 30s timeout (services/backup_restic.go:208); honouring that would
// let the run finish on its own and the control would stop controlling
// anything. entered is closed on the first call, so the control can prove the
// run is blocked HERE rather than having failed somewhere earlier for an
// unrelated reason — a positive control for the injection itself.
type blockingResticRunner struct {
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func (r *blockingResticRunner) block() {
	r.enteredOnce.Do(func() { close(r.entered) })
	<-r.release
}

func (r *blockingResticRunner) Run(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
	_ chan<- services.StreamLine,
) error {
	r.block()
	return nil
}

func (r *blockingResticRunner) Output(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
) ([]byte, error) {
	r.block()
	return []byte(`{}`), nil
}

// TestRegistry_AttachUnfinishedRun_IsNotReportedDone is the control arm for
// TestRegistry_AttachFinishedRun: a durable run that GENUINELY never completes
// must still be reported as not-Done by awaitDurableRun.
//
// Without it, converting the wait from a wall-clock poll to a channel wait
// could have removed the assertion instead of stabilising it — a waiter that
// reported the hang guard as success would make the test above pass forever.
// SEEN FAILING under a -overlay that changes awaitDurableRun's guard branch
// from `return nil, false` to `return ar, true`: this test failed on its
// require.False at backup_test.go:1701 while TestRegistry_AttachFinishedRun
// stayed green in the same run, which is precisely the hole it exists to
// close (agent-os-25ye, 2026-09-05, `go test -race -count=1`).
//
// The 500ms bound below is a wall-clock bound, but it points the SAFE way: the
// run cannot finish by construction (the injected runner blocks until this
// test's own cleanup releases it, and that release is proven to be the only
// exit by the entered handshake), so a slower machine makes "still not done"
// MORE certain, not less. That is the opposite of the load sensitivity being
// fixed above, where slowness turned a correct result into a failure.
func TestRegistry_AttachUnfinishedRun_IsNotReportedDone(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, true) // both bins set; RunSync needs both
	require.NoError(t, db.SetSetting("rclone_remote", "fakeprovider"))
	require.NoError(t, db.SetSetting("restic_repository", "/tmp/test-repo"))
	require.NoError(t, db.SetSetting("restic_password", "control-arm-password"))

	runner := &blockingResticRunner{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, slog.Default())
	})

	reg := services.NewBackupRunnerRegistry(db, svc, slog.Default())
	t.Cleanup(func() {
		// Release before stopping: Stop waits on the run's WaitGroup, so a
		// still-blocked runner would deadlock the cleanup.
		close(runner.release)
		reg.StopWithTimeout(30 * time.Second)
	})

	// RunSync reaches the restic manager first: runSyncInternal calls
	// restic.CheckRepository before it ever builds the rclone manager
	// (services/backup.go:965).
	runID, err := reg.LaunchSync()
	require.NoError(t, err)

	select {
	case <-runner.entered:
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("the injected restic runner was never reached: this control is not blocking where it claims to")
	}

	// wall-clock ok: the bound IS the assertion. This is a bounded NEGATIVE
	// probe -- it asserts the run has NOT finished -- so a slow box can only
	// push it further toward the expected answer, never turn correct code red,
	// which is the opposite direction from the flake class agent-os-jar5 and
	// agent-os-euyg close. hangGuardDeadline(t) here would cost the full
	// wsHangGuardCeiling (60s) on every run and discriminate nothing extra:
	// a registry that wrongly reported Done would do so immediately.
	_, ok := awaitDurableRun(t, reg, runID, time.Now().Add(500*time.Millisecond)) // wall-clock ok: bounded negative probe, see above
	require.False(t, ok,
		"a durable run that never completes must NOT be reported as Done — "+
			"if this passes, the wait no longer asserts anything")
}

// panicCommandRunner is a fake CommandRunner that panics on every call.
// It is used to verify that recoverExec catches a panic inside an exec goroutine
// and finalises the run as "failed" rather than crashing the process.
//
// Output panics too, not just Run: the first thing RunSync reaches is
// ResticManager.CheckRepository -> runner.Run (services/backup.go:967,
// backup_restic.go:216), but if that ordering ever changes the panic must
// still fire on whichever method is hit first, or this test silently stops
// reaching its subject again (agent-os-ev4m).
type panicCommandRunner struct{}

const injectedPanicValue = "injected panic for recoverExec test"

func (r *panicCommandRunner) Run(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
	_ chan<- services.StreamLine,
) error {
	panic(injectedPanicValue)
}

func (r *panicCommandRunner) Output(
	_ context.Context,
	_ string,
	_ []string,
	_ []string,
) ([]byte, error) {
	panic(injectedPanicValue)
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
//
// HISTORY (agent-os-ev4m). This test used to build its service with
// resticPresent=false and inject the panicking runner only at the rclone
// seam. That was correct when written, but agent-os-h0my later made RunSync
// require BOTH binaries (services/backup.go:912), so RunSync returned
// ErrBackupUnavailable before any runner was constructed — and "failed" was
// still asserted true, because ErrBackupUnavailable ALSO produces "failed".
// PROBED, not read: with atomic counters in the fake runner, Run calls=0,
// Output calls=0, ErrorMessage="backup engine unavailable", test PASS. The
// assertion could not tell the two paths apart, so recoverExec went untested
// while looking tested. Hence the exact-reason assertions below: only
// recoverExec writes the "panic: " prefix (backup_runner.go:901), every
// other failure path writes err.Error() verbatim.
func TestRegistry_PanicInExec_RunTerminatesAsFailed(t *testing.T) {
	// Not parallel — injects a panicking runner; must not interfere with other tests.

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, true) // BOTH bins set: RunSync gates on both (services/backup.go:912)

	// Inject the panicking runner at BOTH manager seams. CheckRepository (via
	// the restic manager) is reached first; the rclone seat stays armed so a
	// reordering cannot un-reach the panic (see panicCommandRunner).
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, &panicCommandRunner{}, slog.Default())
	})
	svc.SetRcloneMgrFactory(func(bc services.BackupConfig) *services.RcloneManager {
		return services.NewRcloneManagerForTest(bc, &panicCommandRunner{}, slog.Default())
	})

	// Provide the minimum config so RunSync does not return early before
	// reaching the runner: the remote (runSyncInternal's first check) and a
	// restic password (CheckRepository calls withPasswordFile BEFORE
	// runner.Run and refuses an empty one — a second "failed the easy way"
	// path this test must not be satisfied by).
	require.NoError(t, db.SetSetting("rclone_remote", "fakeprovider"))
	require.NoError(t, db.SetSetting("restic_repository", "/tmp/test-repo"))
	require.NoError(t, db.SetSetting("restic_password", "panic-test-password"))

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
	// awaitDurableRun waits on the run's own completion channel (dr.done)
	// rather than polling Attach under a fixed budget, so the PASS depends on
	// the goroutine finishing, never on how fast the box is (agent-os-jar5;
	// the poll this replaces is the shape agent-os-25ye turned red in CI).
	finalAR, finished := awaitDurableRun(t, reg, runID, hangGuardDeadline(t))
	require.True(t, finished,
		"run must reach Done=true after the panic, before the hang guard")

	assert.Equal(t, "failed", finalAR.Outcome,
		"outcome must be 'failed' when the exec goroutine panics")

	// The discriminating assertion: recoverExec formats the recovered value as
	// "panic: %v" (backup_runner.go:901) and that exact string is what Attach
	// reports as Reason. ErrBackupUnavailable ("backup engine unavailable"),
	// a missing password, or a runner that merely RETURNS an error all reach
	// "failed" through execSync's normal error branch, which stores
	// err.Error() verbatim and never carries the "panic: " prefix. Seen
	// failing with the panic replaced by a returned error of the same text:
	// Reason = "cannot access restic repository: injected panic ...".
	wantReason := "panic: " + injectedPanicValue
	assert.Equal(t, wantReason, finalAR.Reason,
		"Attach must report the recovered panic value; anything else means the run failed without ever panicking")

	// Confirm the DB record was also updated, with the same recovered value.
	dbRun, dbErr := db.GetBackupRunByID(runID)
	require.NoError(t, dbErr)
	assert.Equal(t, "failed", dbRun.Status,
		"DB status must be 'failed', not 'running', after a panic in the exec goroutine")
	assert.Equal(t, wantReason, dbRun.ErrorMessage,
		"finaliseRunStatus must persist the recovered panic value as the run's error message")
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
	// false when the repository must look reachable.
	//
	// It does NOT affect the snapshot listing: that is `snapshots --json`, a
	// separate invocation reaching Output() rather than Run(), and failListing
	// below is the field that fails it. The two are deliberately independent —
	// a healthy repository can still fail a listing (agent-os-rg8h).
	failRepoProbe bool

	// failListing makes `restic snapshots --json` — the LISTING path, through
	// Output() — fail with exit code listingExitCode.
	//
	// It exists because before agent-os-rg8h this fixture had no way to fail a
	// listing at all: Output() returned []byte(`[]`), nil unconditionally, so
	// every test that thought it was exercising the listing path was reading a
	// fabricated success. The uninitialised 500 this wave fixes was invisible
	// to CI for exactly that reason.
	//
	// It is a SEPARATE field from failRepoProbe on purpose: every pre-existing
	// arm sets only failRepoProbe and must keep the behaviour it was written
	// against.
	failListing bool

	// listingExitCode is the exit code a failed listing reports, defaulting to
	// 10 to match repoProbeExitCode's default. The handler does not
	// discriminate on it — any non-zero listing exit is an error ListSnapshots
	// wraps — so it exists for readability, not for branching.
	listingExitCode int

	// repoProbeExitCode is the process exit code the failed probe reports, and
	// it defaults to 10 because CheckRepository now discriminates on it
	// (agent-os-81vr): 10 is restic's "repository does not exist", which is the
	// state failRepoProbe's own comment describes, while any other code means
	// the repository exists but could not be read and repoInit must refuse to
	// create over it. MEASURED with restic 0.18.0: `snapshots --quiet` exits 10
	// against a path holding no repository, 0 against an initialised but empty
	// one, and 1 against one whose directory is unreadable.
	repoProbeExitCode int

	// failLs makes `restic ls` (the preview path, through Run()) fail with exit
	// 1, which is what restic 0.18.0 exits with for an id naming no snapshot
	// AND for other fatal errors alike — so the exit code cannot tell them
	// apart (agent-os-uh8y).
	failLs bool

	// listingJSON, when non-empty, is what a successful `restic snapshots
	// --json` returns instead of `[]`.
	listingJSON string
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
	// `snapshots --json` (the listing path) is a different invocation, it
	// arrives at Output() rather than here, and this flag must never decide its
	// outcome — failListing does, and independently.
	if r.failRepoProbe && argsContainAll(args, []string{"snapshots", "--quiet"}) {
		code := r.repoProbeExitCode
		if code == 0 {
			code = 10
		}
		return fakeExitError{code: code}
	}
	if r.failLs && len(args) > 0 && args[0] == "ls" {
		return fakeExitError{code: 1}
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
	if r.failListing && argsContainAll(args, []string{"snapshots", "--json"}) {
		code := r.listingExitCode
		if code == 0 {
			code = 10
		}
		return nil, fakeExitError{code: code}
	}
	if r.listingJSON != "" && argsContainAll(args, []string{"snapshots", "--json"}) {
		return []byte(r.listingJSON), nil
	}
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
// /app/data/restic-repo and a repository state of "not initialised".
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

// TestUpdateSettings_SavingDoesNotKillRunningScheduledScheduler pins the
// SEQUENCE rather than an end state. The bug this task removes was a RUNNING
// scheduled-mode scheduler being stopped and then not restarted, so the test
// has to start one first: an assertion made from a never-started state cannot
// see that class of failure at all, and would pass against a build that never
// starts the scheduler under any circumstances.
//
// Interval 0 throughout, because that is the configuration an operator lands
// in after switching to a fixed time, and the one every interval-shaped guard
// gets wrong.
func TestUpdateSettings_SavingDoesNotKillRunningScheduledScheduler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		// The literal sequence: an ordinary save of something unrelated.
		{"unrelated field", map[string]interface{}{"keepDaily": 30}},
		// The discriminating one: a schedule field DOES stop the scheduler
		// first, so the restart is what keeps it alive.
		{"schedule field", map[string]interface{}{"scheduleTime": "23:45"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newBackupHandlerDB(t)
			require.NoError(t, db.SetSetting("backup_schedule_mode", "scheduled"))
			require.NoError(t, db.SetSetting("backup_schedule_time", "02:00"))
			// backup_schedule_interval deliberately never set → resolves to 0.

			svc := buildBackupSvc(t, db, true, false)
			h := NewBackupHandler(svc, db, slog.Default())
			t.Cleanup(h.Stop)
			r := newBackupRouter(h)

			sched := &handlerFakeScheduler{}
			svc.SetScheduler(sched)

			// Boot the scheduler the way main.go does, and prove it is really
			// running before the save — otherwise the assertion afterwards
			// proves nothing.
			svc.StartScheduler()
			require.True(t, svc.SchedulerRunning(), "precondition: the scheduler must be running before the save")
			require.NotNil(t, sched.scheduled, "precondition: it must be running on the scheduled path")

			req := jsonReq(t, http.MethodPut, "/api/settings/backup", tc.body)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

			assert.True(t, svc.SchedulerRunning(),
				"saving %s must leave the scheduled-mode scheduler running; a zero interval is not 'disabled' in this mode", tc.name)
			assert.NotNil(t, sched.scheduled, "it must still be on the scheduled path")

			// And the status endpoint must agree, since that is what the
			// operator actually sees.
			statusReq := httptest.NewRequest(http.MethodGet, "/api/backups/status", nil)
			statusW := httptest.NewRecorder()
			r.ServeHTTP(statusW, statusReq)
			require.Equal(t, http.StatusOK, statusW.Code)
			assert.Equal(t, true, decodeBody(t, statusW)["schedulerRunning"],
				"the status endpoint must still report the scheduler as running")
		})
	}
}

// ─────────────────────────────────────────────
// Backup history pagination + filters (agent-os-lak4.1)
// ─────────────────────────────────────────────

// newBackupHistoryRouter is the shared fixture for the tests below: a wired
// BackupHandler over a migrated DB, plus the DB itself so a test can seed rows.
func newBackupHistoryRouter(t *testing.T) (*gin.Engine, *database.DB) {
	t.Helper()
	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine has finished its DB
	// write, so it must run BEFORE db.Close(); t.Cleanup is LIFO and this is
	// registered last. See agent-os-80n.
	t.Cleanup(h.Stop)
	return newBackupRouter(h), db
}

// seedBackupRuns inserts n runs with DISTINCT started_at values, newest last.
// Distinct timestamps are load-bearing: getHistory orders by started_at DESC
// with no tiebreaker, so equal timestamps would make any page-window assertion
// non-deterministic rather than merely arbitrary.
func seedBackupRuns(t *testing.T, db *database.DB, n int) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		require.NoError(t, db.CreateBackupRun(&models.BackupRun{
			ID:        fmt.Sprintf("run-%03d", i),
			Kind:      "backup",
			Trigger:   "manual",
			Status:    "success",
			StartedAt: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		}))
	}
}

func getBackupHistory(t *testing.T, r *gin.Engine, query string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/backups/history"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return decodeBody(t, w)
}

// TestBackupHistory_PaginatesWithTotal is AC1.
func TestBackupHistory_PaginatesWithTotal(t *testing.T) {
	t.Parallel()

	r, db := newBackupHistoryRouter(t)
	seedBackupRuns(t, db, 25)

	body := getBackupHistory(t, r, "?page=2&limit=10")

	runs, ok := body["runs"].([]interface{})
	require.True(t, ok, "runs must stay an array")
	assert.Len(t, runs, 10, "page 2 of 25 at limit 10 holds 10 runs")
	assert.Equal(t, float64(2), body["page"])
	assert.Equal(t, float64(10), body["limit"])
	assert.Equal(t, float64(25), body["total"])
	assert.Equal(t, float64(3), body["totalPages"])

	// The window must be the SECOND ten, not the first: a handler that ignored
	// page would return the right COUNT with the wrong rows.
	first, ok := runs[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "run-014", first["id"], "page 2 starts at the 11th newest run")
}

// TestBackupHistory_NoParamsReturnsRecentRuns is AC4, the backward-compatibility
// guard. Per the brief's fact D this has no fail-first arm: the no-params
// behaviour is already correct today, so this pins it rather than adding it.
func TestBackupHistory_NoParamsReturnsRecentRuns(t *testing.T) {
	t.Parallel()

	r, db := newBackupHistoryRouter(t)
	seedBackupRuns(t, db, 3)

	body := getBackupHistory(t, r, "")

	runs, ok := body["runs"].([]interface{})
	require.True(t, ok, "runs must remain an array under the same key")
	require.Len(t, runs, 3)
	newest, ok := runs[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "run-002", newest["id"], "most recent run first")
}

// TestBackupHistory_ClampsLimitToMax is AC5. The seed is deliberately 101 rows:
// at 100 an unclamped handler and a clamped one return the same length, so the
// assertion could not discriminate.
func TestBackupHistory_ClampsLimitToMax(t *testing.T) {
	t.Parallel()

	r, db := newBackupHistoryRouter(t)
	seedBackupRuns(t, db, 101)

	body := getBackupHistory(t, r, "?limit=100000")

	runs, ok := body["runs"].([]interface{})
	require.True(t, ok)
	assert.Len(t, runs, 100, "limit is capped at 100 regardless of the request")
	assert.Equal(t, float64(100), body["limit"], "the clamped limit is what is reported back")
	assert.Equal(t, float64(101), body["total"], "total still counts every matching run")
}

// seedDecorrelatedRuns inserts four runs whose kind, trigger and status cut
// ACROSS each other rather than partitioning the table the same way, so every
// filter value selects a different set of ids:
//
//	id      kind    trigger    status   started_at
//	run-a   backup  manual     success  2026-01-01
//	run-b   backup  scheduled  failed   2026-03-15
//	run-c   prune   scheduled  failed   2026-06-01
//	run-d   prune   manual     failed   2026-09-01
//
// With only two anti-correlated rows every filter returns "one row or the
// other", so a clause bound to the wrong column still selects the expected row
// and the test cannot say which column it filtered on. Here it changes the
// answer. Mirrors seedFilterRuns in database/backup_test.go.
func seedDecorrelatedRuns(t *testing.T, db *database.DB) {
	t.Helper()
	rows := []models.BackupRun{
		{ID: "run-a", Kind: "backup", Trigger: "manual", Status: "success", StartedAt: "2026-01-01T00:00:00Z"},
		{ID: "run-b", Kind: "backup", Trigger: "scheduled", Status: "failed", StartedAt: "2026-03-15T00:00:00Z"},
		{ID: "run-c", Kind: "prune", Trigger: "scheduled", Status: "failed", StartedAt: "2026-06-01T00:00:00Z"},
		{ID: "run-d", Kind: "prune", Trigger: "manual", Status: "failed", StartedAt: "2026-09-01T00:00:00Z"},
	}
	for i := range rows {
		require.NoError(t, db.CreateBackupRun(&rows[i]))
	}
}

// runIDsFromBody pulls the ordered id list out of a history response.
func runIDsFromBody(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	runs, ok := body["runs"].([]interface{})
	require.True(t, ok, "runs must be an array")
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		m, ok := r.(map[string]interface{})
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		ids = append(ids, id)
	}
	return ids
}

// TestBackupHistory_FiltersReachTheQuery proves each query parameter is wired to
// its own column, both ways on one instrument: the whole seed with the filter
// absent, and EXACTLY that column's set with the filter present.
//
// Asserting the exact set rather than "one row came back" is what pins the
// column — over a decorrelated seed no two filters share an answer, so a clause
// bound to a neighbouring column changes the result.
func TestBackupHistory_FiltersReachTheQuery(t *testing.T) {
	t.Parallel()

	r, db := newBackupHistoryRouter(t)
	seedDecorrelatedRuns(t, db)

	allIDs := []string{"run-d", "run-c", "run-b", "run-a"}

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"status", "?status=failed", []string{"run-d", "run-c", "run-b"}},
		{"kind", "?kind=backup", []string{"run-b", "run-a"}},
		{"trigger", "?trigger=scheduled", []string{"run-c", "run-b"}},
		{"from", "?from=2026-08-01T00:00:00Z", []string{"run-d"}},
		{"to", "?to=2026-07-01T00:00:00Z", []string{"run-c", "run-b", "run-a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Control arm: unfiltered, the whole seed is visible, so a short
			// result below is the filter and not an empty table.
			all := getBackupHistory(t, r, "")
			require.Equal(t, float64(len(allIDs)), all["total"], "every run visible unfiltered")
			require.Equal(t, allIDs, runIDsFromBody(t, all))
			require.Less(t, len(tc.want), len(allIDs),
				"a filter that excludes nothing cannot demonstrate filtering")

			body := getBackupHistory(t, r, tc.query)
			assert.Equal(t, tc.want, runIDsFromBody(t, body), "exact matching set, newest first")
			assert.Equal(t, float64(len(tc.want)), body["total"], "total reflects the filter")
		})
	}
}

// TestBackupHistory_HugePageDoesNotWrapToPageOne is the HTTP-level arm of the
// OFFSET overflow guard. page = MaxInt64 is accepted by strconv.Atoi, and
// (page-1)*limit wraps to a negative offset that SQLite reads as no offset —
// serving page ONE while echoing back the enormous page number. A wrong answer
// dressed as a correct one.
func TestBackupHistory_HugePageDoesNotWrapToPageOne(t *testing.T) {
	t.Parallel()

	r, db := newBackupHistoryRouter(t)
	seedDecorrelatedRuns(t, db)

	// Control arm: page one over the same seed is populated, so the empty
	// result below cannot be an empty table.
	first := getBackupHistory(t, r, "?page=1&limit=50")
	require.NotEmpty(t, runIDsFromBody(t, first), "page one is non-empty")

	body := getBackupHistory(t, r, "?page=9223372036854775807&limit=50")
	assert.Empty(t, runIDsFromBody(t, body), "a page past the end must never return page one's rows")
	assert.Equal(t, float64(4), body["total"], "total still describes the whole match set")
}

// ============================================================
// agent-os-81vr — the three repository states on the wire
// ============================================================

// fakeExitError simulates os/exec's *exec.ExitError so a test can drive the
// exit code BackupService.CheckRepository discriminates on.
//
// It is a second copy of the double with the same name in
// internal/services/backup_test.go, and deliberately so: that one is
// unexported in package services and unreachable from package handlers. The
// shape is the contract (an "ExitCode() int" method, matched by isExitCode's
// exitCoder interface via errors.As), not the type.
type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e fakeExitError) ExitCode() int { return e.code }

// backupProbeRouter wires a BackupHandler whose restic probe (`restic
// snapshots --quiet`) exits with probeExitCode. Zero means the probe succeeds,
// i.e. the repository is reachable.
//
// Every arm of the agent-os-81vr tests runs on this ONE instrument, varying
// only the exit code, so a difference in the response is attributable to the
// repository state and not to a differently built fixture.
func backupProbeRouter(t *testing.T, probeExitCode int) (*gin.Engine, *recordingResticRunner) {
	t.Helper()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_password", "test-repo-password"))

	svc := buildBackupSvc(t, db, true, false)
	runner := &recordingResticRunner{
		failRepoProbe:     probeExitCode != 0,
		repoProbeExitCode: probeExitCode,
	}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, slog.Default())
	})

	h := NewBackupHandler(svc, db, slog.Default())
	// See agent-os-80n: h.Stop() must run before the DB and TempDir cleanups
	// registered above, and t.Cleanup runs LIFO.
	t.Cleanup(h.Stop)
	return newBackupRouter(h), runner
}

// backupNoPasswordRouter is backupProbeRouter's sibling for the ONE state that
// cannot be expressed by a probe exit code: no restic password configured.
//
// It deliberately does not seed restic_password, which is the default state of
// a fresh install — both shipped compose files leave RESTIC_PASSWORD commented
// out.
//
// MEASURED, by removing CheckRepository's password check and re-running: with
// this fixture the repository then reports RepoStateUnreachable carrying
// "repository not reachable: restic password is not configured". NOT "perfectly
// healthy", which is what an earlier draft of this comment claimed — the probe
// runner is wired to succeed, but ResticManager.withPasswordFile refuses an
// empty password before the runner is ever reached, so the failure is a
// MISATTRIBUTED fault rather than a hidden one. That misattribution, sending an
// operator to check a remote and a mount that are both fine, IS the defect
// agent-os-l04z was filed for, and it is what this fixture catches.
func backupNoPasswordRouter(t *testing.T) (*gin.Engine, *recordingResticRunner) {
	t.Helper()

	db := newBackupHandlerDB(t)

	svc := buildBackupSvc(t, db, true, false)
	runner := &recordingResticRunner{}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, runner, slog.Default())
	})

	h := NewBackupHandler(svc, db, slog.Default())
	// See agent-os-80n: h.Stop() must run before the DB and TempDir cleanups
	// registered above, and t.Cleanup runs LIFO.
	t.Cleanup(h.Stop)
	return newBackupRouter(h), runner
}

// repoStateOf returns the repoState an error response carries in its details.
func repoStateOf(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	details, ok := body["details"].(map[string]interface{})
	require.True(t, ok, "error body must carry a details object naming the repository state, got %v", body)
	state, ok := details["repoState"].(string)
	require.True(t, ok, "details must carry repoState, got %v", details)
	return state
}

// TestListSnapshots_EmptyRepositoryVsUnreachable is the load-bearing arm of
// agent-os-81vr, and it is TWO-SIDED on one instrument.
//
// Before the fix, an UNREACHABLE repository was answered with 200 and an empty
// array — byte-identical to "you have never taken a backup" — so a user whose
// repository had gone away was shown no backups and invited to initialise one.
// The fix must NOT be bought by erroring on the empty case too: a repository
// that is initialised with zero snapshots exits 0 and is a legitimate 200 [].
// An implementation that fails both arms passes half of acceptance criterion 2
// and is worse than the bug.
func TestListSnapshots_EmptyRepositoryVsUnreachable(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	t.Run("initialised and empty still answers 200 with an empty array", func(t *testing.T) {
		r, _ := backupProbeRouter(t, 0)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

		require.Equal(t, http.StatusOK, w.Code,
			"a reachable repository with zero snapshots is a genuine empty state, not a fault")
		var snapshots []models.BackupSnapshot
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snapshots))
		assert.Empty(t, snapshots)
	})

	// agent-os-rg8h. This sub-test used to assert 200 and an empty array, and it
	// was green for the wrong reason: recordingResticRunner.Output() returned
	// []byte(`[]`), nil UNCONDITIONALLY, so the listing path could not fail in
	// any test and the 500 an uninitialised repository really produced was
	// invisible to CI. failListing is what makes the listing capable of failing
	// at all, and it is left ON here on purpose — it is now the assertion that
	// the short-circuit happens BEFORE restic is called, because a handler that
	// still reached the listing would answer 500 rather than 409.
	//
	// OBSERVED with failListing wired and the handler unfixed:
	//   --- FAIL: …/never_initialised_also_answers_200_with_an_empty_array
	//       Error: Not equal: expected: 200 / actual: 500
	t.Run("never initialised answers 409 and names the state", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 10)
		runner.failListing = true

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

		require.Equal(t, http.StatusConflict, w.Code,
			"a repository that does not exist yet is a named, recoverable state, not a server fault")
		body := decodeBody(t, w)
		assert.Equal(t, models.ErrBackupRepoUninitialized, body["code"])
		assert.Equal(t, "uninitialized", repoStateOf(t, body))
		message, _ := body["message"].(string)
		assert.NotEmpty(t, message,
			"the sentence CheckRepository already computed must be surfaced, not discarded")
		assert.NotContains(t, message, "Failed to list snapshots",
			"the old answer reported a server fault for an ordinary fresh-install state")
	})

	// The other side of the same instrument, and the arm that keeps
	// failListing alive. After the short-circuit above, NOTHING else in the
	// permanent suite exercises a failing listing — a dead fixture field is
	// invisible to --- FAIL counting, and worse, the short-circuit could
	// silently swallow GENUINE listing failures with no gate noticing.
	//
	// probeExitCode 0 means the repository is healthy and initialised, so the
	// handler reaches the listing; failListing then fails it.
	//
	// WHAT THIS ARM STANDS FOR. "CheckRepository said ok, the listing failed
	// anyway" is not a contrived pairing — there are at least six production
	// routes to it, and the first is the one worth knowing:
	//
	//  1. TWO INDEPENDENT CONFIG RESOLUTIONS, milliseconds apart. OBSERVED:
	//     CheckRepository resolves at services/backup.go:625
	//     (resolveOrRefuse("check repository")), then the listing resolves
	//     AGAIN at services/backup.go:296 (resolveOrRefuse("build restic
	//     manager")), reached through handlers/backup.go's
	//     listSnapshotsViaRestic. A database fault between the two is already
	//     modelled by backup_config_dbfault_test.go.
	//  2. withPasswordFile's filesystem arms — CreateTemp, Chmod, WriteString,
	//     Close — on a full disk, a read-only /tmp, or fd exhaustion.
	//  3. TOCTOU: the probe's timeout is 30s and the listing's is 60s, and they
	//     are separate calls against a remote that can go away between them.
	//  4. Different argv: the probe issues `snapshots --quiet`, the listing
	//     `snapshots --json` plus --tag and --latest.
	//  5. A caller-supplied --tag value restic rejects.
	//  6. json.Unmarshal failing AFTER a clean exit 0 — a failure mode no exit
	//     code can represent, so no probe could ever have predicted it.
	//
	// Guarding the short-circuit against all six is what this arm does; without
	// it the D2 branch could swallow every one of them and stay green.
	t.Run("a genuine listing failure on a healthy repository still answers 500", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failListing = true

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

		require.Equal(t, http.StatusInternalServerError, w.Code,
			"the uninitialized short-circuit must not swallow real listing failures")
		body := decodeBody(t, w)
		assert.Equal(t, "Failed to list snapshots", body["message"])
	})

	t.Run("unreachable is distinguishable from empty", func(t *testing.T) {
		r, _ := backupProbeRouter(t, 1)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

		require.Equal(t, http.StatusServiceUnavailable, w.Code,
			"an unreachable repository must not be reported as an ordinary empty list")
		body := decodeBody(t, w)
		assert.Equal(t, "BACKUP_REPO_UNREACHABLE", body["code"])
		assert.Equal(t, "unreachable", repoStateOf(t, body))
		assert.NotEmpty(t, body["message"], "the human sentence CheckRepository already computes must be surfaced")
	})
}

// TestListSnapshots_ResticAbsentIsAFaultNotAnEmptyList pins agent-os-9f5c.
//
// listSnapshots answered a MISSING RESTIC BINARY with 200 and an empty array —
// byte-identical to "you have never taken a backup" — five lines above the
// comment agent-os-81vr added declaring that the empty 200 is reserved for
// repositories that genuinely hold no snapshots. Available() computed the fault
// flag AND the cause sentence in the same branch and the handler discarded
// both: the same struct, the same Message field, the same caller and the same
// file as the 81vr fix, which its close sweep never looked upward to find.
//
// The shape is 409 BACKUP_UNAVAILABLE, NOT BACKUP_REPO_UNREACHABLE: the
// repository is not the thing that failed, and on this path CheckRepository is
// never called, so reachability has not even been asked. details.cause says
// which binary, and there is deliberately no repoState — claiming one would
// assert a probe result nobody obtained.
func TestListSnapshots_ResticAbsentIsAFaultNotAnEmptyList(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	t.Run("restic absent is distinguishable from an empty repository", func(t *testing.T) {
		r := backupNoResticRouter(t)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

		require.Equal(t, http.StatusConflict, w.Code,
			"an absent backup engine must not be reported as an ordinary empty list")
		body := decodeBody(t, w)
		assert.Equal(t, "BACKUP_UNAVAILABLE", body["code"])
		assert.NotEqual(t, "BACKUP_REPO_UNREACHABLE", body["code"],
			"the repository is not the thing that failed, and was never contacted")
		assert.Equal(t, "restic binary not found in PATH", body["message"],
			"the cause Available() already computed must be surfaced, not discarded for a literal")

		details, ok := body["details"].(map[string]interface{})
		require.True(t, ok, "the answer must name which binary is missing, got %v", body)
		assert.Equal(t, "restic_missing", details["cause"])
		_, claimsRepoState := details["repoState"]
		assert.False(t, claimsRepoState,
			"CheckRepository is never called on this path, so no repository state may be claimed")
	})

	// The other side, on the same endpoint: the empty 200 still exists and
	// still belongs to a repository that is reachable, initialised and holds
	// nothing for this stack. An implementation that errors on both arms passes
	// half of acceptance criterion 2 and is worse than the bug. The third
	// 200-[] path — reachable and initialised, but --tag stackID matching
	// nothing, the ORDINARY case for a new stack in a shared repository — takes
	// this same branch and is covered by the stackId arm.
	t.Run("restic present and repository genuinely empty still answers 200", func(t *testing.T) {
		r, _ := backupProbeRouter(t, 0)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots?stackId=some-stack", nil))

		require.Equal(t, http.StatusOK, w.Code,
			"a reachable, initialised repository with no snapshots for this stack is a genuine empty state")
		var snapshots []models.BackupSnapshot
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snapshots))
		assert.Empty(t, snapshots)
	})
}

// TestBackupUnavailableHasOneShapeEverywhere pins the consistency defect found
// by the adversary pass on this wave's own diff.
//
// This wave folded previewSnapshot's 404 into listSnapshots' 409 on the stated
// grounds that two CODES for one state in one handler file is the defect
// agent-os-rg8h was filed about — and then shipped two SHAPES for one code in
// that same file: listSnapshots carried details.cause and the other five
// BACKUP_UNAVAILABLE sites were bare. A client branching on details.cause would
// have had to know WHICH endpoint it asked to know whether the field was there,
// which is the same failure one level down.
//
// All six now go through engineUnavailable(). This test is what stops them
// drifting apart again: "one code, one shape" is otherwise an unenforced claim
// in a docblock, and the six sites are far enough apart in the file that
// nothing else would notice.
//
// The fixture has BOTH binaries absent, which also pins the derived cause. The
// two rclone-guarded endpoints refuse because rclone is missing, but Available()
// returns EARLY on absent restic, so the sentence they ship is restic's — and
// the cause must agree with that sentence rather than with the guard that
// fired, or the body would contradict itself.
func TestBackupUnavailableHasOneShapeEverywhere(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]interface{}
	}{
		{"listSnapshots", http.MethodGet, "/api/backups/snapshots", nil},
		{"previewSnapshot", http.MethodGet, "/api/backups/snapshots/abc12345/preview", nil},
		{"repoInit", http.MethodPost, "/api/backups/repo/init", map[string]interface{}{}},
		// confirm:true is REQUIRED here and is not fixture noise. runDRRestore
		// checks its destructive-operation confirmation BEFORE the availability
		// guard, so an empty body answers 400 CONFIRMATION_REQUIRED and never
		// reaches the branch under test — which would read as this test finding
		// a defect when it had only failed to arrive.
		{"runDRRestore", http.MethodPost, "/api/backups/dr-restore", map[string]interface{}{"confirm": true}},
		{"cloudTest", http.MethodPost, "/api/backups/cloud/test", map[string]interface{}{}},
		{"requireAvailable via runBackup", http.MethodPost, "/api/backups/run", map[string]interface{}{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := backupNoResticRouter(t)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq(t, tc.method, tc.path, tc.body))

			require.Equal(t, http.StatusConflict, w.Code)
			body := decodeBody(t, w)
			assert.Equal(t, "BACKUP_UNAVAILABLE", body["code"])

			details, ok := body["details"].(map[string]interface{})
			require.True(t, ok,
				"every BACKUP_UNAVAILABLE carries details; a client must not have to know which endpoint it asked. got %v", body)
			assert.Equal(t, "restic_missing", details["cause"])

			// The half that proves cause is DERIVED and not hardcoded per
			// site: it must name the same binary the sentence does, including
			// at the two endpoints whose own guard is about rclone.
			assert.Equal(t, "restic binary not found in PATH", body["message"],
				"the cause and the sentence must never disagree about which binary is missing")
		})
	}
}

// TestBackupRepoCredentialStatesAreNamed pins agent-os-l04z on the wire, and it
// is TWO-SIDED on one instrument in the direction that matters: the two new
// credential states must be named, AND a genuinely unreachable repository must
// STILL report unreachable. An implementation that reclassifies every failure
// as a credential problem passes the first half and is worse than the bug.
//
// Both new states route through repoCouldNotBeRead, which is a POSITIVE
// whitelist shared with previewSnapshot. That is why previewSnapshot is asserted
// here too rather than assumed: a state added to RepoState and not added to that
// helper falls out of it silently — no compiler diagnostic, no failing test —
// and previewSnapshot then runs restic with no password and answers 500.
func TestBackupRepoCredentialStatesAreNamed(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	const snapshotID = "abc12345"
	endpoints := []string{
		"/api/backups/snapshots",
		"/api/backups/snapshots/" + snapshotID + "/preview",
	}

	t.Run("no password configured", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint, func(t *testing.T) {
				r, runner := backupNoPasswordRouter(t)

				w := httptest.NewRecorder()
				r.ServeHTTP(w, jsonReq(t, http.MethodGet, endpoint, nil))

				require.Equal(t, http.StatusServiceUnavailable, w.Code,
					"the fresh-install state must be a named fault, not a 500 and not an empty list")
				body := decodeBody(t, w)
				assert.Equal(t, "BACKUP_REPO_UNREACHABLE", body["code"])
				assert.Equal(t, "password_missing", repoStateOf(t, body),
					"the state is what the UI branches on to choose a recovery")
				message, _ := body["message"].(string)
				assert.NotContains(t, message, "not reachable",
					"nothing was contacted; sending an operator to check the remote or the mount is the bug")

				// NOT a pin on this fix, and labelled that way on purpose.
				// MEASURED by mutation: with CheckRepository's password check
				// removed, the two assertions above go red and THIS ONE STAYS
				// GREEN, because withPasswordFile refuses before the runner is
				// reached either way. No handler-level assertion on runner.calls
				// can discriminate this fix — the property is invariant across
				// it. The ordering proof lives one layer down, at
				// services/backup_test.go's "password missing" sub-test, where
				// buildSvc's factory ignores its BackupConfig and hardcodes a
				// password, so absent the check restic genuinely IS invoked.
				//
				// It is kept because it still guards something real, just not
				// this: if withPasswordFile were ever relaxed to tolerate an
				// empty password, restic would start being invoked with no
				// credential and this would catch it.
				assert.Empty(t, runner.calls,
					"restic must not be invoked with no password configured; "+
						"it answers an empty password with exit 1, which is "+
						"indistinguishable from a genuine I/O failure")
			})
		}
	})

	t.Run("password rejected by the repository", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint, func(t *testing.T) {
				r, _ := backupProbeRouter(t, 12)

				w := httptest.NewRecorder()
				r.ServeHTTP(w, jsonReq(t, http.MethodGet, endpoint, nil))

				require.Equal(t, http.StatusServiceUnavailable, w.Code)
				body := decodeBody(t, w)
				assert.Equal(t, "BACKUP_REPO_UNREACHABLE", body["code"])
				assert.Equal(t, "wrong_password", repoStateOf(t, body),
					"restic exit 12 is a clean discriminator and must not collapse into unreachable")
				message, _ := body["message"].(string)
				assert.NotContains(t, message, "not reachable",
					"the repository answered and was read far enough to try the key; only the credential is wrong")
			})
		}
	})

	// The control arm. Exit 1 is a genuine I/O failure and must be untouched by
	// this change — this is the assertion that a reclassifying implementation
	// fails.
	t.Run("a genuinely unreachable repository still reports unreachable", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint, func(t *testing.T) {
				r, _ := backupProbeRouter(t, 1)

				w := httptest.NewRecorder()
				r.ServeHTTP(w, jsonReq(t, http.MethodGet, endpoint, nil))

				require.Equal(t, http.StatusServiceUnavailable, w.Code)
				body := decodeBody(t, w)
				assert.Equal(t, "unreachable", repoStateOf(t, body),
					"widening the credential states must not narrow what unreachable still means")
			})
		}
	})
}

// TestPreviewSnapshot_ReportsWhichCauseHolds pins acceptance criterion 3: the
// handler used to answer "Repository not initialized or unreachable", naming
// two causes and committing to neither. Whichever cause actually holds is now
// the one reported.
func TestPreviewSnapshot_ReportsWhichCauseHolds(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	const snapshotID = "abc12345"

	// agent-os-rg8h folded this from 404 NOT_FOUND into the same 409 that
	// listSnapshots answers for the same CheckRepository value. The 404's own
	// argument was that the snapshot is genuinely absent — but the sentence it
	// shipped described the REPOSITORY, not the snapshot. (A second reason,
	// that classifyError replaced `message` on 404, stopped holding with
	// agent-os-mc4i.)
	//
	// The 404 that now means "unknown snapshot id" is a different branch,
	// reached only after restic was asked (agent-os-uh8y); see
	// TestPreviewSnapshot_UnknownIDIsNotFound.
	t.Run("never initialised", func(t *testing.T) {
		r, _ := backupProbeRouter(t, 10)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots/"+snapshotID+"/preview", nil))

		require.Equal(t, http.StatusConflict, w.Code,
			"one state must have one shape: listSnapshots answers this same CheckRepository value with 409")
		body := decodeBody(t, w)
		assert.Equal(t, models.ErrBackupRepoUninitialized, body["code"])
		assert.Equal(t, "uninitialized", repoStateOf(t, body))
		message, _ := body["message"].(string)
		assert.NotContains(t, message, " or ",
			"the message must name the cause that holds, not enumerate candidates")
		assert.Contains(t, strings.ToLower(message), "initialis",
			"the uninitialised cause must be the one named")
	})

	t.Run("unreachable", func(t *testing.T) {
		r, _ := backupProbeRouter(t, 1)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots/"+snapshotID+"/preview", nil))

		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		body := decodeBody(t, w)
		assert.Equal(t, "BACKUP_REPO_UNREACHABLE", body["code"])
		assert.Equal(t, "unreachable", repoStateOf(t, body))
		message, _ := body["message"].(string)
		assert.NotContains(t, message, " or ",
			"the message must name the cause that holds, not enumerate candidates")
	})
}

// TestRepoInit_CreatesOnlyWhenUninitialised pins acceptance criterion 5, the
// destructive-adjacent arm this bead was filed for.
//
// repoInit used to branch on !RepoReachable, which is true for BOTH "never
// initialised" and "exists but could not be read". Creating a repository
// because the configured one could not be read points every later backup at a
// new, empty repository while the real one still exists.
func TestRepoInit_CreatesOnlyWhenUninitialised(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	t.Run("uninitialised creates", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 10)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/repo/init", map[string]interface{}{}))

		require.Equal(t, http.StatusOK, w.Code)
		_, ok := runner.repoFor("init")
		assert.True(t, ok, "a repository that does not exist must still be created")
	})

	t.Run("unreachable refuses to create", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 1)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/repo/init", map[string]interface{}{}))

		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		body := decodeBody(t, w)
		assert.Equal(t, "BACKUP_REPO_UNREACHABLE", body["code"])
		assert.Equal(t, "unreachable", repoStateOf(t, body))

		_, ok := runner.repoFor("init")
		assert.False(t, ok,
			"a repository that exists but could not be read must NOT be created over")
	})

	t.Run("already initialised does not re-create", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/repo/init", map[string]interface{}{}))

		require.Equal(t, http.StatusOK, w.Code)
		_, ok := runner.repoFor("init")
		assert.False(t, ok, "an already-reachable repository must not be initialised again")
	})
}

// TestRepoStateDiscriminatesRepositoryStatesOnTheWire replaces
// TestRepositoryInitializedReportsWhatItsNameSays (agent-os-ssqt).
// repositoryInitialized was one boolean over four distinct facts: its name
// asserted "a repository exists" while its value measured "the probe answered",
// so a repository that existed but had gone unreachable reported false and the
// UI offered to create one. It is DELETED rather than renamed, because
// repoState already carries the whole distinction and a second field that never
// disagrees with the first is the thing to remove.
//
// The restic-absent row pins the response SHAPE rather than a value. Both
// handlers build their body as a gin.H literal and copy the field across by
// hand, so the `omitempty` on BackupAvailability.RepoState never applies: the
// key ships as "" instead of being absent. The frontend union has to admit ”
// because of this, which is why the row is here and not just in prose.
func TestRepoStateDiscriminatesRepositoryStatesOnTheWire(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	cases := []struct {
		name      string
		router    func(t *testing.T) *gin.Engine
		repoState string
	}{
		{"reachable", func(t *testing.T) *gin.Engine { r, _ := backupProbeRouter(t, 0); return r }, "ok"},
		{"never initialised", func(t *testing.T) *gin.Engine { r, _ := backupProbeRouter(t, 10); return r }, "uninitialized"},
		{"exists but unreadable", func(t *testing.T) *gin.Engine { r, _ := backupProbeRouter(t, 1); return r }, "unreachable"},
		{"restic absent", backupNoResticRouter, ""},
	}

	for _, endpoint := range []string{"/api/backups/status", "/api/settings/backup"} {
		for _, tc := range cases {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				r := tc.router(t)

				w := httptest.NewRecorder()
				r.ServeHTTP(w, jsonReq(t, http.MethodGet, endpoint, nil))

				require.Equal(t, http.StatusOK, w.Code)
				body := decodeBody(t, w)

				state, present := body["repoState"]
				require.True(t, present,
					"repoState must be on the wire in every state, including the one that never probed")
				assert.Equal(t, tc.repoState, state,
					"the state must be on the wire so a client can tell the fault cases apart")

				_, stale := body["repositoryInitialized"]
				assert.False(t, stale,
					"repositoryInitialized asserted a distinction its value did not carry; it is deleted, not renamed")

				// A healthy repository explains nothing, and it must not
				// explain something ELSE. backupProbeRouter builds its service
				// with buildBackupSvc(t, db, true, false) — restic present,
				// RCLONE ABSENT — which is the exact install on which
				// Available()'s rclone cause (added in this wave so the two
				// "rclone is not available" handlers have a sentence to
				// forward) would otherwise ride through CheckRepository's OK
				// branch untouched and be shipped here as the explanation for a
				// repository that is fine. types/index.ts documents
				// repoStateMessage as "Empty when there is none", and both
				// dashboard surfaces render it, so this arm is what keeps that
				// true. OBSERVED red on exactly this assertion after the
				// Available() change and before CheckRepository's OK branch
				// cleared the message.
				if tc.repoState == "ok" {
					assert.Equal(t, "", body["repoStateMessage"],
						"a repository in the ok state has nothing to explain, and must not borrow an unrelated fault's sentence")
				}
			})
		}
	}
}

// backupNoResticRouter builds the handler with no restic binary — the one state
// in which CheckRepository returns before probing, leaving RepoState empty.
func backupNoResticRouter(t *testing.T) *gin.Engine {
	t.Helper()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, false, false)

	h := NewBackupHandler(svc, db, slog.Default())
	// See agent-os-80n: h.Stop() must run before the DB and TempDir cleanups
	// registered above, and t.Cleanup runs LIFO.
	t.Cleanup(h.Stop)
	return newBackupRouter(h)
}

// listingArgs returns the argv of the first `restic snapshots --json` call the
// runner recorded, and whether there was one.
func (r *recordingResticRunner) listingArgs() ([]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if argsContainAll(c.args, []string{"snapshots", "--json"}) {
			return c.args, true
		}
	}
	return nil, false
}

// TestListSnapshots_ValidatesStackID pins agent-os-qh3g: stackId goes into
// restic's argv as the value of --tag, so it is held to the stack-ID charset
// (middleware.ValidateStackID) and a length bound before restic is called.
//
// It is NOT resolved against the stacks table, and the "unknown id" row below
// is what pins that. Deleting a stack leaves its snapshots in the repository,
// and a re-IDed stack leaves snapshots tagged with its old ID; both must stay
// listable by filter.
func TestListSnapshots_ValidatesStackID(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	listURL := func(stackID string) string {
		return "/api/backups/snapshots?" + url.Values{"stackId": {stackID}}.Encode()
	}

	rejected := map[string]string{
		// OBSERVED with restic 0.18.0: `--tag a,b` is an AND of two tags, so a
		// comma silently changes what the filter means.
		"comma":        "stacks~web,capstan-backup",
		"slash":        "../etc",
		"newline":      "stacks~web\nx",
		"over the cap": strings.Repeat("a", maxStackIDLen+1),
		// OBSERVED with restic 0.18.0: a 200000-byte tag fails execve with
		// "Argument list too long", which answered 500.
		"past MAX_ARG_STRLEN": strings.Repeat("a", 200000),
	}
	for name, stackID := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			r, runner := backupProbeRouter(t, 0)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq(t, http.MethodGet, listURL(stackID), nil))

			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrValidation, body["code"])
			assert.Equal(t, "Invalid stack ID", body["message"])
			_, listed := runner.listingArgs()
			assert.False(t, listed, "a rejected stackId must never reach restic")
		})
	}

	t.Run("well-formed id with no snapshots answers 200 and an empty array", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)

		const stackID = "stacks~deleted-long-ago:app"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, listURL(stackID), nil))

		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var snapshots []models.BackupSnapshot
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snapshots))
		assert.Empty(t, snapshots)
		args, listed := runner.listingArgs()
		require.True(t, listed)
		assert.Equal(t, []string{"snapshots", "--json", "--tag", stackID}, args)
	})

	t.Run("id at the cap is accepted", func(t *testing.T) {
		r, _ := backupProbeRouter(t, 0)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, listURL(strings.Repeat("a", maxStackIDLen)), nil))

		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	})

	t.Run("empty id still lists everything, with no tag filter", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots", nil))

		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		args, listed := runner.listingArgs()
		require.True(t, listed)
		assert.Equal(t, []string{"snapshots", "--json"}, args)
	})
}

// TestPreviewSnapshot_UnknownIDIsNotFound pins agent-os-uh8y: a well-formed id
// naming no snapshot answers 404, not 500. restic has no exit code for "not
// found" (OBSERVED with 0.18.0: exit 1 for an unknown prefix, for an unknown
// full id and for "latest" on an empty repository, and the message differs by
// storage backend), so the handler decides it from a successful listing
// instead. Every other preview failure stays 500, and the rows below pin both
// sides.
func TestPreviewSnapshot_UnknownIDIsNotFound(t *testing.T) {
	// Not parallel — injects a manager factory on the service.

	const existing = `[{"id":"aa1e6e99ac47207dc3f58912a79c11f2ca1140c3976899a589954f833e6b997f","short_id":"aa1e6e99","time":"2026-09-24T12:00:00Z","tags":["stacks~web"],"paths":["/stacks/web"]}]`

	preview := func(t *testing.T, r *gin.Engine, id string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq(t, http.MethodGet, "/api/backups/snapshots/"+id+"/preview", nil))
		return w
	}

	t.Run("unknown id against a repository that lists fine answers 404", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failLs = true
		runner.listingJSON = existing

		w := preview(t, r, "deadbeef")

		require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
		body := decodeBody(t, w)
		assert.Equal(t, models.ErrNotFound, body["code"])
		assert.Equal(t, "Snapshot deadbeef not found in the backup repository", body["message"])
	})

	t.Run("latest against a repository with no snapshots answers 404", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failLs = true

		w := preview(t, r, "latest")

		require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, models.ErrNotFound, decodeBody(t, w)["code"])
	})

	t.Run("preview fails and the listing fails too: 500", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failLs = true
		runner.failListing = true
		runner.listingExitCode = 1

		w := preview(t, r, "deadbeef")

		require.Equal(t, http.StatusInternalServerError, w.Code,
			"absence is only claimed from a listing that succeeded")
		assert.Equal(t, "Failed to preview snapshot", decodeBody(t, w)["message"])
	})

	t.Run("preview fails for a snapshot that exists: 500", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failLs = true
		runner.listingJSON = existing

		// Upper case and longer than the short id: restic matches ids by
		// prefix, case-insensitively as hex, so this still names the snapshot.
		w := preview(t, r, "AA1E6E99AC47")

		require.Equal(t, http.StatusInternalServerError, w.Code,
			"a snapshot that is listed exists; its preview failing is a server fault")
		assert.Equal(t, "Failed to preview snapshot", decodeBody(t, w)["message"])
	})

	t.Run("latest fails while snapshots exist: 500", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failLs = true
		runner.listingJSON = existing

		w := preview(t, r, "latest")

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("malformed id still answers 400 without calling restic", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.failLs = true

		w := preview(t, r, "not-a-snapshot")

		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "Invalid snapshot ID", decodeBody(t, w)["message"])
		_, listed := runner.listingArgs()
		assert.False(t, listed)
	})

	t.Run("existing snapshot previews with 200", func(t *testing.T) {
		r, runner := backupProbeRouter(t, 0)
		runner.listingJSON = existing

		w := preview(t, r, "aa1e6e99")

		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		_, listed := runner.listingArgs()
		assert.False(t, listed, "the happy path must not pay for a second restic call")
	})
}

// TestUpdateSettings_RcloneRemoteLeadingDash pins agent-os-tyl6: the remote is
// rclone's first positional, and a value starting with '-' is parsed as a flag
// (OBSERVED with rclone v1.60.1: "--log-file=/p" created the file "/p:"). It is
// refused before ANY field of the request is written.
func TestUpdateSettings_RcloneRemoteLeadingDash(t *testing.T) {
	t.Parallel()

	put := func(t *testing.T, body map[string]interface{}) (*httptest.ResponseRecorder, *database.DB) {
		t.Helper()
		db := newBackupHandlerDB(t)
		svc := buildBackupSvc(t, db, true, false)
		h := NewBackupHandler(svc, db, slog.Default())
		t.Cleanup(h.Stop)
		w := httptest.NewRecorder()
		newBackupRouter(h).ServeHTTP(w, jsonReq(t, http.MethodPut, "/api/settings/backup", body))
		return w, db
	}

	for _, remote := range []string{"--log-file=/tmp/x", "-x"} {
		t.Run("rejects "+remote, func(t *testing.T) {
			t.Parallel()
			w, db := put(t, map[string]interface{}{"rcloneRemote": remote, "repository": "/data/other-repo"})

			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrValidation, body["code"])
			assert.Equal(t, "rclone remote must not start with '-'", body["message"])
			for _, key := range []string{"rclone_remote", "restic_repository"} {
				stored, err := db.GetSetting(key)
				assert.Empty(t, stored, "%s must not be written by a rejected request (err=%v)", key, err)
			}
		})
	}

	// Accepting side: ordinary names, an inner '-', and an on-the-fly
	// connection-string remote (":s3,..."), which rclone's name rules would
	// not cover and which works today.
	for _, remote := range []string{"myremote", "my-remote", ":s3,provider=AWS", ""} {
		t.Run("accepts "+remote, func(t *testing.T) {
			t.Parallel()
			w, db := put(t, map[string]interface{}{"rcloneRemote": remote})

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			stored, err := db.GetSetting("rclone_remote")
			require.NoError(t, err)
			assert.Equal(t, remote, stored)
		})
	}
}
