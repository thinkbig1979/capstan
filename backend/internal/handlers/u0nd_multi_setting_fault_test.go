package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// A handler that saves several settings from one request must save all of
// them or none (agent-os-u0nd). Each writer is driven with two keys, and a
// trigger makes the storage write of the LATER key fail. Before the fix the
// earlier key's write was already committed when the later one failed, so the
// form came back 500 with half of it applied.
//
// The fault is a trigger on a second handle rather than a fake: every writer
// holds a concrete *database.DB. It fires only for the named key, so the
// earlier key's write succeeds on its own and only the transaction can undo it.
//
// Each case also runs a CONTROL: the same two keys with no fault must both be
// written. Without it, a handler that writes nothing at all would pass the
// fault arm.

type u0ndWriter struct {
	name      string
	path      string
	body      string
	earlier   string // key written first
	later     string // key whose write is made to fail
	newEarly  string // value the request stores under earlier
	newLater  string // value the request stores under later
	okStatus  int    // the writer's success code
	newRouter func(t *testing.T, db *database.DB) *gin.Engine
}

func u0ndWriters() []u0ndWriter {
	settingsRouter := func(t *testing.T, db *database.DB) *gin.Engine {
		t.Helper()
		h := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil, &config.Config{})
		r := gin.New()
		r.Use(authContextMiddleware("test-user-id"))
		h.RegisterRoutes(r.Group("/api"))
		return r
	}
	return []u0ndWriter{
		{
			name:     "backup updateSettings",
			path:     "/api/settings/backup",
			body:     `{"keepDaily":11,"hostname":"u0nd-host"}`,
			earlier:  "backup_keep_daily",
			later:    "backup_hostname",
			newEarly: "11",
			newLater: "u0nd-host",
			newRouter: func(t *testing.T, db *database.DB) *gin.Engine {
				t.Helper()
				h := NewBackupHandler(buildBackupSvc(t, db, true, false), db, slog.Default())
				t.Cleanup(h.Stop)
				return newBackupRouter(h)
			},
		},
		{
			name:     "docker cleanup updateCleanupPolicy",
			path:     "/api/resources/cleanup/policy",
			body:     `{"enabled":true,"intervalHours":48}`,
			earlier:  services.SettingDockerCleanupEnabled,
			later:    services.SettingDockerCleanupIntervalHours,
			newEarly: "true",
			newLater: "48",
			newRouter: func(t *testing.T, db *database.DB) *gin.Engine {
				t.Helper()
				h := NewResourcesHandler(nil, db, nil)
				r := gin.New()
				r.Use(authContextMiddleware("test-user-id"))
				h.RegisterRoutes(r.Group("/api"))
				return r
			},
		},
		{
			name:      "settings UpdateLogRetention",
			path:      "/api/settings/log-retention",
			body:      `{"retentionDays":45,"cleanupHistoryRetentionDays":46}`,
			earlier:   database.SettingLogRetentionDays,
			later:     database.SettingCleanupHistoryRetentionDays,
			newEarly:  "45",
			newLater:  "46",
			okStatus:  http.StatusNoContent,
			newRouter: settingsRouter,
		},
		{
			name:      "settings UpdateUpdateSettings",
			path:      "/api/settings/updates",
			body:      `{"scanIntervalMinutes":30,"applyDays":[1,3]}`,
			earlier:   "update_scan_interval",
			later:     "update_apply_days",
			newEarly:  "30",
			newLater:  "1,3",
			newRouter: settingsRouter,
		},
		{
			name:      "settings UpdateGitSettings",
			path:      "/api/settings/git",
			body:      `{"sshKey":"/keys/u0nd_ed25519","httpsUser":"u0nd-user"}`,
			earlier:   "git_ssh_key",
			later:     "git_https_user",
			newEarly:  "/keys/u0nd_ed25519",
			newLater:  "u0nd-user",
			newRouter: settingsRouter,
		},
	}
}

const (
	u0ndSeedEarly = "u0nd-seed-early"
	u0ndSeedLater = "u0nd-seed-later"
)

// u0ndDB opens an encryptor-backed, file-backed DB with both keys seeded, and
// returns a second raw handle on the same file for installing the fault.
func u0ndDB(t *testing.T, w u0ndWriter) (*database.DB, *sql.DB) {
	t.Helper()
	dir := newMigratedDBDir(t)
	enc := services.NewTokenEncryptorOrDefault("", "test-secret-32-chars-padding-here")
	db, err := database.NewWithEncryptor(dir, enc)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	createTestUser(t, db, "u0nd-admin", "correct-horse-battery")

	require.NoError(t, db.SetSetting(w.earlier, u0ndSeedEarly))
	require.NoError(t, db.SetSetting(w.later, u0ndSeedLater))

	raw, err := sql.Open("sqlite", filepath.Join(dir, "capstan.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return db, raw
}

func u0ndPut(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func u0ndSetting(t *testing.T, db *database.DB, key string) string {
	t.Helper()
	v, err := db.GetSetting(key)
	require.NoError(t, err, "read %s", key)
	return v
}

func TestMultiSettingWritersAreAllOrNothing(t *testing.T) {
	for _, w := range u0ndWriters() {
		t.Run(w.name+"/control: a clean save writes both keys", func(t *testing.T) {
			db, _ := u0ndDB(t, w)
			rec := u0ndPut(t, w.newRouter(t, db), w.path, w.body)
			want := http.StatusOK
			if w.okStatus != 0 {
				want = w.okStatus
			}
			require.Equal(t, want, rec.Code, rec.Body.String())
			require.Equal(t, w.newEarly, u0ndSetting(t, db, w.earlier))
			require.Equal(t, w.newLater, u0ndSetting(t, db, w.later))
		})

		t.Run(w.name+"/a storage fault on the later key leaves the earlier key unchanged", func(t *testing.T) {
			db, raw := u0ndDB(t, w)
			trigger := "CREATE TRIGGER u0nd_fail_later BEFORE INSERT ON settings WHEN NEW.key = '" + w.later + //nolint:gosec // test fixture: DDL takes no bind parameters and w.later is a constant from u0ndWriters
				"' BEGIN SELECT RAISE(ABORT, 'u0nd injected storage fault'); END"
			_, err := raw.Exec(trigger)
			require.NoError(t, err)

			rec := u0ndPut(t, w.newRouter(t, db), w.path, w.body)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			require.Equal(t, u0ndSeedEarly, u0ndSetting(t, db, w.earlier),
				"%s was committed although %s failed in the same request: the form half-applied", w.earlier, w.later)
			require.Equal(t, u0ndSeedLater, u0ndSetting(t, db, w.later))
		})
	}
}
