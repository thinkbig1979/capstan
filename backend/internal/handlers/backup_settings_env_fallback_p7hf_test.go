package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// agent-os-p7hf: the backup settings form must show the values the engine
// acts on. It used to read the DB rows itself with hardcoded defaults, so with
// BACKUP_KEEP_DAILY=30 and no saved row the form said 7 while restic kept 30.
// An operator who then raised "7" to 10 lowered retention from 30 to 10, and
// the next `restic forget --prune` dropped the difference.

// p7hfSettings is the subset of GET /api/settings/backup these tests read.
// Typed rather than map-decoded so an int field is compared as an int.
type p7hfSettings struct {
	KeepDaily               int    `json:"keepDaily"`
	KeepWeekly              int    `json:"keepWeekly"`
	KeepMonthly             int    `json:"keepMonthly"`
	KeepYearly              int    `json:"keepYearly"`
	AutoPrune               bool   `json:"autoPrune"`
	ScheduleIntervalMinutes int    `json:"scheduleIntervalMinutes"`
	SyncAfterBackup         bool   `json:"syncAfterBackup"`
	RcloneRemote            string `json:"rcloneRemote"`
	RclonePath              string `json:"rclonePath"`
	RcloneTransfers         int    `json:"rcloneTransfers"`
	Hostname                string `json:"hostname"`
}

// p7hfGetSettings builds a handler whose live config is adjusted by setEnv
// (the handler reads env fallbacks only through config.Config, never
// os.Getenv), seeds the given DB rows, and returns the decoded GET response.
func p7hfGetSettings(t *testing.T, setEnv func(*config.Config), rows map[string]string) p7hfSettings {
	t.Helper()

	db := newBackupHandlerDB(t)
	svc := buildBackupSvc(t, db, true, false)
	setEnv(svc.Config())
	for k, v := range rows {
		require.NoError(t, db.SetSetting(k, v))
	}
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got p7hfSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

// Every field with an env fallback, set to a non-default value, no DB rows:
// the form must show the env value, because that is what the engine uses.
func TestBackupGetSettings_ShowsEnvFallbackValues_p7hf(t *testing.T) {
	t.Parallel()

	got := p7hfGetSettings(t, func(c *config.Config) {
		c.BackupKeepDaily = "30"
		c.BackupKeepWeekly = "9"
		c.BackupKeepMonthly = "12"
		c.BackupKeepYearly = "2"
		c.BackupAutoPrune = "false"
		c.BackupScheduleInterval = "90"
		c.BackupSyncAfter = "true"
		c.RcloneRemote = "envremote"
		c.RclonePath = "env/path"
		c.RcloneTransfers = "8"
		c.BackupHostname = "nas-01"
	}, nil)

	assert.Equal(t, 30, got.KeepDaily, "keepDaily from BACKUP_KEEP_DAILY")
	assert.Equal(t, 9, got.KeepWeekly, "keepWeekly from BACKUP_KEEP_WEEKLY")
	assert.Equal(t, 12, got.KeepMonthly, "keepMonthly from BACKUP_KEEP_MONTHLY")
	assert.Equal(t, 2, got.KeepYearly, "keepYearly from BACKUP_KEEP_YEARLY")
	assert.False(t, got.AutoPrune, "autoPrune from BACKUP_AUTO_PRUNE")
	assert.Equal(t, 90, got.ScheduleIntervalMinutes, "scheduleIntervalMinutes from BACKUP_SCHEDULE_INTERVAL")
	assert.True(t, got.SyncAfterBackup, "syncAfterBackup from BACKUP_SYNC_AFTER")
	assert.Equal(t, "envremote", got.RcloneRemote, "rcloneRemote from RCLONE_REMOTE")
	assert.Equal(t, "env/path", got.RclonePath, "rclonePath from RCLONE_PATH")
	assert.Equal(t, 8, got.RcloneTransfers, "rcloneTransfers from RCLONE_TRANSFERS")
	assert.Equal(t, "nas-01", got.Hostname, "hostname from BACKUP_HOSTNAME")
}

// No env, no DB row: hostname is the engine's default, not an empty string.
func TestBackupGetSettings_ShowsEngineDefaultHostname_p7hf(t *testing.T) {
	t.Parallel()

	got := p7hfGetSettings(t, func(*config.Config) {}, nil)
	assert.Equal(t, "capstan", got.Hostname)
}

// A stored value the engine cannot parse falls through to env, so the form
// must too, rather than to its own hardcoded default.
func TestBackupGetSettings_UnparseableRowFallsThroughToEnv_p7hf(t *testing.T) {
	t.Parallel()

	got := p7hfGetSettings(t, func(c *config.Config) {
		c.BackupKeepDaily = "30"
	}, map[string]string{"backup_keep_daily": "abc"})
	assert.Equal(t, 30, got.KeepDaily)
}

// CONTROL: a saved row still wins over env (precedence unchanged).
func TestBackupGetSettings_SavedRowBeatsEnv_p7hf(t *testing.T) {
	t.Parallel()

	got := p7hfGetSettings(t, func(c *config.Config) {
		c.BackupKeepDaily = "30"
		c.RcloneRemote = "envremote"
	}, map[string]string{
		"backup_keep_daily": "10",
		"rclone_remote":     "dbremote",
	})
	assert.Equal(t, 10, got.KeepDaily)
	assert.Equal(t, "dbremote", got.RcloneRemote)
}
