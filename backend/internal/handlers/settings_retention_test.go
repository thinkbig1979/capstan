package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// The retention endpoint used to govern action_log alone. update_history and
// backup_runs grew without bound (agent-os-0jp), so it now carries all three.

func newRetentionRouter(t *testing.T) (*database.DB, http.Handler) {
	t.Helper()
	db, err := database.NewWithMigrations(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	return db, setupSettingsRouter(handler)
}

func putRetention(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestGetLogRetention_ReturnsEveryHistoryTable(t *testing.T) {
	db, router := newRetentionRouter(t)
	require.NoError(t, db.SetSetting(database.SettingUpdateHistoryRetentionDays, "45"))
	require.NoError(t, db.SetSetting(database.SettingBackupHistoryRetentionDays, "30"))

	req := httptest.NewRequest(http.MethodGet, "/settings/log-retention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]int
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, 90, body["retentionDays"], "audit log default")
	assert.Equal(t, 45, body["updateHistoryRetentionDays"])
	assert.Equal(t, 30, body["backupHistoryRetentionDays"])
	assert.Equal(t, database.MinRetentionDays, body["minRetentionDays"])
}

// TestGetLogRetention_ClampsStoredValue: a row edited by hand below the floor
// must still report the value that will actually be applied.
func TestGetLogRetention_ClampsStoredValue(t *testing.T) {
	db, router := newRetentionRouter(t)
	require.NoError(t, db.SetSetting(database.SettingUpdateHistoryRetentionDays, "1"))

	req := httptest.NewRequest(http.MethodGet, "/settings/log-retention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body map[string]int
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, database.MinRetentionDays, body["updateHistoryRetentionDays"])
}

func TestUpdateLogRetention_AcceptsPartialUpdate(t *testing.T) {
	db, router := newRetentionRouter(t)
	require.NoError(t, db.SetSetting(database.SettingLogRetentionDays, "90"))

	w := putRetention(t, router, `{"updateHistoryRetentionDays": 60}`)
	assert.Equal(t, http.StatusNoContent, w.Code)

	value, err := db.GetSetting(database.SettingUpdateHistoryRetentionDays)
	require.NoError(t, err)
	assert.Equal(t, "60", value)

	// The untouched setting is left alone rather than reset to a default.
	value, err = db.GetSetting(database.SettingLogRetentionDays)
	require.NoError(t, err)
	assert.Equal(t, "90", value)
}

func TestUpdateLogRetention_RejectsBelowFloorOnEveryField(t *testing.T) {
	for _, field := range []string{
		"retentionDays", "updateHistoryRetentionDays", "backupHistoryRetentionDays",
	} {
		t.Run(field, func(t *testing.T) {
			db, router := newRetentionRouter(t)

			w := putRetention(t, router, `{"`+field+`": 1}`)
			assert.Equal(t, http.StatusBadRequest, w.Code)

			// Nothing was written, so a rejected request cannot half-apply.
			for _, key := range []string{
				database.SettingUpdateHistoryRetentionDays,
				database.SettingBackupHistoryRetentionDays,
			} {
				value, err := db.GetSetting(key)
				require.NoError(t, err)
				assert.Equal(t, "90", value, "%s should be untouched", key)
			}
		})
	}
}

func TestUpdateLogRetention_RejectsEmptyBody(t *testing.T) {
	_, router := newRetentionRouter(t)

	w := putRetention(t, router, `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "At least one retention value is required")
}
