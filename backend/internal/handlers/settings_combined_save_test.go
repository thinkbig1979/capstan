package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A save that changes
// BOTH the scan interval AND the apply schedule in one request must leave the
// scheduler armed correctly for both. agent-os-mtbo.2 owns the apply re-arm and
// agent-os-mtbo.8 owns the interval restart; nothing exercised them together.
func TestUpdateUpdateSettings_CombinedIntervalAndScheduleSave(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))
	require.NoError(t, db.SetSetting("update_apply_mode", "immediate"))

	// Both dimensions change in ONE request.
	w := putUpdateSettings(t, router,
		`{"scanIntervalMinutes":180,"globalAutoUpdate":true,"applyMode":"scheduled","applyTime":"02:15","applyDays":[2,4]}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// The interval half took effect.
	assert.Equal(t, float64(180), resp["scanIntervalMinutes"], "the new scan interval must be applied")
	stored, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "180", stored)

	// The schedule half took effect in the same request.
	assert.Equal(t, "scheduled", resp["applyMode"])
	assert.Equal(t, "02:15", resp["applyTime"])
	assert.Equal(t, []interface{}{float64(2), float64(4)}, resp["applyDays"])

	// And the scheduler is armed for BOTH: still running after the interval
	// restart, and reporting a next apply instant from the new schedule.
	assert.True(t, sched.IsRunning(), "the scheduler must still be running after a combined save")
	nextApplyAt, ok := resp["nextApplyAt"].(string)
	require.True(t, ok, "a live schedule must report nextApplyAt after a combined save")
	parsed, err := time.Parse(time.RFC3339, nextApplyAt)
	require.NoError(t, err, "nextApplyAt must be RFC3339")
	assert.True(t, parsed.After(time.Now()), "the next apply instant must be in the future")
	assert.Equal(t, 15, parsed.Minute(), "the new applyTime must drive the next instant")
	assert.Contains(t, []time.Weekday{time.Tuesday, time.Thursday}, parsed.Weekday(),
		"the next instant must land on one of the requested weekdays")
}
