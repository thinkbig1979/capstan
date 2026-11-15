package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// agent-os-h71i: a session with env values locked could point the backup
// repository at a place it controls, initialise it, run a backup and read
// every stack's .env from the snapshot. While locked, a request that changes
// where or with what credential backups go, or the retention, initialises a
// repository, restores, DR-restores or prunes is refused with ENV_LOCKED and
// changes nothing. Status, listing, running a backup to the configured
// repository and schedule edits stay allowed.

// lockedBackupRouterH71i mounts the backup routes with no unlock mark: locked.
func lockedBackupRouterH71i(h *BackupHandler) *gin.Engine {
	r := gin.New()
	h.RegisterRoutes(r.Group("/api"))
	return r
}

func newBackupHandlerH71i(t *testing.T) (*BackupHandler, *database.DB) {
	t.Helper()
	db := newBackupHandlerDB(t)
	// restic and rclone absent: nothing can launch, so a request the lock
	// does not refuse answers "engine unavailable" instead of running.
	h := NewBackupHandler(buildBackupSvc(t, db, false, false), db, slog.Default())
	t.Cleanup(h.Stop)
	return h, db
}

// settingOrAbsentH71i is the stored value, or "<absent>" when no row exists.
func settingOrAbsentH71i(t *testing.T, db *database.DB, key string) string {
	t.Helper()
	v, err := db.GetSetting(key)
	if errors.Is(err, errdefs.ErrNotFound) {
		return "<absent>"
	}
	require.NoError(t, err)
	return v
}

func envLockedCodeH71i(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		return "<not json: " + err.Error() + ">"
	}
	return body.Code
}

// Each destination, credential or retention field, alone, is refused while
// locked and its setting is left as it was.
func TestBackupSettings_LockedRefusesDestinationAndRetention_h71i(t *testing.T) {
	fields := []struct {
		field string
		value any
		key   string
	}{
		{"repository", "/attacker/repo", "restic_repository"},
		{"password", "attacker-chosen-pw", "restic_password"},
		{"rcloneRemote", "attacker:", "rclone_remote"},
		{"rclonePath", "elsewhere", "rclone_path"},
		{"keepDaily", 1, "backup_keep_daily"},
		{"keepWeekly", 1, "backup_keep_weekly"},
		{"keepMonthly", 1, "backup_keep_monthly"},
		{"keepYearly", 1, "backup_keep_yearly"},
		{"autoPrune", true, "backup_auto_prune"},
	}
	for _, f := range fields {
		t.Run(f.field, func(t *testing.T) {
			h, db := newBackupHandlerH71i(t)
			before := settingOrAbsentH71i(t, db, f.key)

			w := httptest.NewRecorder()
			lockedBackupRouterH71i(h).ServeHTTP(w, jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]any{f.field: f.value}))
			assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.Equal(t, "ENV_LOCKED", envLockedCodeH71i(t, w), w.Body.String())

			assert.Equal(t, before, settingOrAbsentH71i(t, db, f.key), "a refused save changes nothing")
		})
	}
}

// A schedule or hostname edit moves no data to a new reader and stays
// allowed while locked; unlocked, a destination change still saves.
func TestBackupSettings_LockedAllowsScheduleUnlockedSavesAll_h71i(t *testing.T) {
	h, db := newBackupHandlerH71i(t)
	w := httptest.NewRecorder()
	lockedBackupRouterH71i(h).ServeHTTP(w, jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]any{"scheduleIntervalMinutes": 0, "hostname": "box"}))
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	w = httptest.NewRecorder()
	newBackupRouter(h).ServeHTTP(w, jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]any{"repository": "/data/repo", "keepDaily": 3}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got, err := db.GetSetting("restic_repository")
	require.NoError(t, err)
	assert.Equal(t, "/data/repo", got)
}

// Restore, DR restore, prune and repository init are refused while locked.
// Unlocked, the same request reaches the engine check, which answers here
// because restic and rclone are absent: the refusal comes from the lock alone.
func TestBackupOps_LockedRefused_h71i(t *testing.T) {
	ops := []struct {
		name, path string
		body       any
	}{
		{"restore", "/api/backups/restore", map[string]any{"stackId": "s1", "snapshotId": "abc123", "confirm": true}},
		{"dr-restore", "/api/backups/dr-restore", map[string]any{"confirm": true}},
		{"prune", "/api/backups/prune", map[string]any{"confirm": true}},
		{"repo init", "/api/backups/repo/init", nil},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			h, _ := newBackupHandlerH71i(t)
			w := httptest.NewRecorder()
			lockedBackupRouterH71i(h).ServeHTTP(w, jsonReq(t, http.MethodPost, op.path, op.body))
			assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.Equal(t, "ENV_LOCKED", envLockedCodeH71i(t, w), w.Body.String())

			w = httptest.NewRecorder()
			newBackupRouter(h).ServeHTTP(w, jsonReq(t, http.MethodPost, op.path, op.body))
			assert.NotEqual(t, http.StatusForbidden, w.Code, "unlocked, the lock does not refuse: %s", w.Body.String())
		})
	}
}
