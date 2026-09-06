package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

func setupSettingsRouter(handler *SettingsHandler) *gin.Engine {
	router := gin.New()
	router.GET("/settings/log-retention", handler.GetLogRetention)
	router.PUT("/settings/log-retention", handler.UpdateLogRetention)
	return router
}

func TestSettingsHandler_GetLogRetention_Default(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/settings/log-retention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, float64(90), response["retentionDays"])
}

func TestSettingsHandler_GetLogRetention_Custom(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	err = db.SetSetting("max_log_retention_days", "60")
	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/settings/log-retention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, float64(60), response["retentionDays"])
}

func TestSettingsHandler_UpdateLogRetention(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": 45}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	value, err := db.GetSetting("max_log_retention_days")
	require.NoError(t, err)
	assert.Equal(t, "45", value)
}

func TestSettingsHandler_UpdateLogRetention_Minimum(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": 7}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	value, err := db.GetSetting("max_log_retention_days")
	require.NoError(t, err)
	assert.Equal(t, "7", value)
}

func TestSettingsHandler_UpdateLogRetention_BelowMinimum(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": 5}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response models.AppError
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "VALIDATION_ERROR", response.Code)
}

func TestSettingsHandler_UpdateLogRetention_InvalidInput(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(tempDir)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, err)

	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := setupSettingsRouter(handler)

	reqBody := `{"retentionDays": "invalid"}`
	req := httptest.NewRequest(http.MethodPut, "/settings/log-retention", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func setupUpdateSettingsRouter(handler *SettingsHandler) *gin.Engine {
	router := gin.New()
	router.GET("/settings/updates", handler.GetUpdateSettings)
	router.PUT("/settings/updates", handler.UpdateUpdateSettings)
	return router
}

// newUpdateSettingsFixture wires PUT /settings/updates to a real SchedulerService
// so the scheduler side effects of a partial payload are observable via IsRunning().
func newUpdateSettingsFixture(t *testing.T) (*gin.Engine, *database.DB, *services.SchedulerService) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	checker := &handlerTestChecker{block: make(chan struct{})}
	sched := services.NewSchedulerService(checker, db, nil, nil)
	t.Cleanup(func() {
		close(checker.block)
		sched.Stop()
	})

	handler := NewSettingsHandler(db, "", "test-secret", false, sched, nil)
	return setupUpdateSettingsRouter(handler), db, sched
}

func putUpdateSettings(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/settings/updates", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// Control: a full payload still applies both values and still starts the scheduler.
func TestSettingsHandler_UpdateUpdateSettings_FullPayloadApplies(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":60,"globalAutoUpdate":true}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval)
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate)
	assert.True(t, sched.IsRunning(), "a supplied non-zero interval must start the scheduler")
}

// Control: a full payload asking for interval 0 still stops the scheduler.
func TestSettingsHandler_UpdateUpdateSettings_FullPayloadDisables(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":0,"globalAutoUpdate":false}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "0", interval)
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "false", autoUpdate)
	assert.False(t, sched.IsRunning(), "an explicit interval of 0 must stop the scheduler")
}

// Regression (agent-os-mtbo.8): a PUT that omits both keys must change nothing.
func TestSettingsHandler_UpdateUpdateSettings_PartialPayloadPreservesBoth(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"applyMode":"scheduled"}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval, "an absent scanIntervalMinutes must not reset the interval")
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate, "an absent globalAutoUpdate must not disable auto-update")
	assert.True(t, sched.IsRunning(), "an absent scanIntervalMinutes must not stop the scheduler")

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, float64(60), response["scanIntervalMinutes"])
	assert.Equal(t, true, response["globalAutoUpdate"])
}

// Regression (agent-os-mtbo.8): supplying only globalAutoUpdate must leave the
// interval and the running scheduler alone.
func TestSettingsHandler_UpdateUpdateSettings_AutoUpdateOnlyPreservesInterval(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "false"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"globalAutoUpdate":true}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval, "an absent scanIntervalMinutes must not reset the interval")
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate)
	assert.True(t, sched.IsRunning(), "an absent scanIntervalMinutes must not stop the scheduler")
}

// Regression (agent-os-mtbo.8): supplying only scanIntervalMinutes must leave
// auto-update alone.
func TestSettingsHandler_UpdateUpdateSettings_IntervalOnlyPreservesAutoUpdate(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "30"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":60}`)
	assert.Equal(t, http.StatusOK, w.Code)

	interval, err := db.GetSetting("update_scan_interval")
	require.NoError(t, err)
	assert.Equal(t, "60", interval)
	autoUpdate, err := db.GetSetting("auto_update_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", autoUpdate, "an absent globalAutoUpdate must not disable auto-update")
	assert.True(t, sched.IsRunning(), "a changed non-zero interval must restart the scheduler")
}

// ---------------------------------------------------------------------------
// Scheduled apply settings (agent-os-mtbo.2).
// ---------------------------------------------------------------------------

// TestSettingsHandler_GetUpdateSettings_DefaultsFromMigration checks the shape a
// fresh install serves: immediate mode, the seeded 03:00 every day, and the
// server's own zone so the operator can see which clock that time means.
func TestSettingsHandler_GetUpdateSettings_DefaultsFromMigration(t *testing.T) {
	router, _, _ := newUpdateSettingsFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/settings/updates", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	assert.Equal(t, "immediate", response["applyMode"])
	assert.Equal(t, "03:00", response["applyTime"])
	assert.Equal(t, []interface{}{
		float64(0), float64(1), float64(2), float64(3), float64(4), float64(5), float64(6),
	}, response["applyDays"])
	assert.NotEmpty(t, response["serverTimezone"])
	assert.NotEmpty(t, response["serverTimeOffset"])
	assert.NotContains(t, response, "nextApplyAt", "immediate mode has no next apply instant")
}

// TestSettingsHandler_UpdateUpdateSettings_ApplyScheduleRoundTrips is the trap-D
// test: gin silently accepts unknown JSON fields, so a struct-tag typo would
// ship green and the value would simply never arrive. Only a PUT through the
// real handler followed by a GET through the real handler proves the wire names
// are the ones the merged frontend sends.
func TestSettingsHandler_UpdateUpdateSettings_ApplyScheduleRoundTrips(t *testing.T) {
	router, db, _ := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	require.NoError(t, db.SetSetting("auto_update_enabled", "true"))

	w := putUpdateSettings(t, router, `{"applyMode":"scheduled","applyTime":"04:30","applyDays":[1,3,5]}`)
	require.Equal(t, http.StatusOK, w.Code)

	// The PUT's own response body is the GET projection, so it round-trips here.
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "scheduled", response["applyMode"])
	assert.Equal(t, "04:30", response["applyTime"])
	assert.Equal(t, []interface{}{float64(1), float64(3), float64(5)}, response["applyDays"])

	// nextApplyAt appears only once an apply is actually going to happen:
	// scheduled mode, auto-update on, and a non-zero scan interval.
	nextApplyAt, ok := response["nextApplyAt"].(string)
	require.True(t, ok, "a live schedule must report its next apply instant")
	parsed, err := time.Parse(time.RFC3339, nextApplyAt)
	require.NoError(t, err, "nextApplyAt must be RFC3339")
	assert.True(t, parsed.After(time.Now()), "the next apply instant must be in the future")
	assert.Equal(t, 30, parsed.Minute())

	// And a fresh GET reads back exactly the same thing from storage.
	req := httptest.NewRequest(http.MethodGet, "/settings/updates", nil)
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, req)
	require.Equal(t, http.StatusOK, getW.Code)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &got))
	assert.Equal(t, "scheduled", got["applyMode"])
	assert.Equal(t, "04:30", got["applyTime"])
	assert.Equal(t, []interface{}{float64(1), float64(3), float64(5)}, got["applyDays"])

	stored, err := db.GetSetting("update_apply_days")
	require.NoError(t, err)
	assert.Equal(t, "1,3,5", stored, "days are stored in the schedule helpers' normalised form")
}

// TestSettingsHandler_UpdateUpdateSettings_ApplyFieldsAreOptional guards the
// merged settings screen's contract: it sends applyMode/applyTime/applyDays only
// when the admin actually touched the schedule, so absent must mean unchanged.
func TestSettingsHandler_UpdateUpdateSettings_ApplyFieldsAreOptional(t *testing.T) {
	router, db, _ := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_apply_mode", "scheduled"))
	require.NoError(t, db.SetSetting("update_apply_time", "04:30"))
	require.NoError(t, db.SetSetting("update_apply_days", "1,3,5"))

	w := putUpdateSettings(t, router, `{"scanIntervalMinutes":60}`)
	require.Equal(t, http.StatusOK, w.Code)

	mode, err := db.GetSetting("update_apply_mode")
	require.NoError(t, err)
	assert.Equal(t, "scheduled", mode, "an absent applyMode must not reset the mode")
	applyTime, err := db.GetSetting("update_apply_time")
	require.NoError(t, err)
	assert.Equal(t, "04:30", applyTime, "an absent applyTime must not reset the time")
	days, err := db.GetSetting("update_apply_days")
	require.NoError(t, err)
	assert.Equal(t, "1,3,5", days, "an absent applyDays must not reset the days")
}

// TestSettingsHandler_UpdateUpdateSettings_InvalidApplyFields covers every
// rejection: mode, time and days each return the standard 400 shape, and none
// of them writes anything on the way out.
func TestSettingsHandler_UpdateUpdateSettings_InvalidApplyFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"unknown mode", `{"applyMode":"hourly"}`},
		{"empty mode", `{"applyMode":""}`},
		{"time without a colon", `{"applyTime":"0300"}`},
		{"hour out of range", `{"applyTime":"25:00"}`},
		{"minute out of range", `{"applyTime":"03:60"}`},
		{"weekday out of range", `{"applyDays":[0,7]}`},
		{"negative weekday", `{"applyDays":[-1]}`},
		{"no weekday at all", `{"applyDays":[]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, db, _ := newUpdateSettingsFixture(t)

			w := putUpdateSettings(t, router, tc.body)
			require.Equal(t, http.StatusBadRequest, w.Code)

			var appErr models.AppError
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &appErr))
			assert.Equal(t, "VALIDATION_ERROR", appErr.Code)

			// A rejected body must leave the seeded defaults untouched.
			mode, err := db.GetSetting("update_apply_mode")
			require.NoError(t, err)
			assert.Equal(t, "immediate", mode)
			applyTime, err := db.GetSetting("update_apply_time")
			require.NoError(t, err)
			assert.Equal(t, "03:00", applyTime)
			days, err := db.GetSetting("update_apply_days")
			require.NoError(t, err)
			assert.Equal(t, "0,1,2,3,4,5,6", days)
		})
	}

	// Control on the same instrument: the valid form of each field is accepted,
	// so the 400s above are the validator working rather than the handler
	// rejecting the whole payload shape.
	t.Run("control: the valid form of all three is accepted", func(t *testing.T) {
		router, _, _ := newUpdateSettingsFixture(t)
		w := putUpdateSettings(t, router, `{"applyMode":"scheduled","applyTime":"03:00","applyDays":[0,6]}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestSettingsHandler_GetUpdateSettings_ApplyDaysNeverMarshalsNull covers the
// frontend's hard requirement, two-sided.
//
// The zero-value control is the load-bearing half: it shows the field carries no
// omitempty and that a nil slice really does become null, so the handler's
// []int{} initialisation is what produces the array — not JSON encoding luck.
func TestSettingsHandler_GetUpdateSettings_ApplyDaysNeverMarshalsNull(t *testing.T) {
	// Control: left nil, this field marshals to null.
	zeroValue, err := json.Marshal(models.UpdateSettingsResponse{})
	require.NoError(t, err)
	require.Contains(t, string(zeroValue), `"applyDays":null`,
		"control: a nil ApplyDays marshals to null, which is what the handler must prevent")

	// Attack: a stored day list the parser rejects. The handler must still serve
	// an array, not null and not a silently substituted default.
	router, db, _ := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_apply_days", "not,a,weekday"))

	req := httptest.NewRequest(http.MethodGet, "/settings/updates", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Contains(t, w.Body.String(), `"applyDays":[]`,
		"an unparseable stored day list must still serve an empty array")
	assert.NotContains(t, w.Body.String(), `"applyDays":null`)
}

// TestSettingsHandler_GetUpdateSettings_NextApplyAtOmitted pins each condition
// that suppresses nextApplyAt, with the all-conditions-met control alongside so
// the omissions are not just "this build never emits it".
func TestSettingsHandler_GetUpdateSettings_NextApplyAtOmitted(t *testing.T) {
	scheduled := func(t *testing.T, db *database.DB) {
		t.Helper()
		require.NoError(t, db.SetSetting("update_apply_mode", "scheduled"))
		require.NoError(t, db.SetSetting("update_scan_interval", "60"))
		require.NoError(t, db.SetSetting("auto_update_enabled", "true"))
	}

	cases := []struct {
		name    string
		mutate  func(t *testing.T, db *database.DB)
		present bool
	}{
		{"control: scheduled, enabled, scanning", func(*testing.T, *database.DB) {}, true},
		{"immediate mode", func(t *testing.T, db *database.DB) {
			require.NoError(t, db.SetSetting("update_apply_mode", "immediate"))
		}, false},
		{"auto-update off", func(t *testing.T, db *database.DB) {
			require.NoError(t, db.SetSetting("auto_update_enabled", "false"))
		}, false},
		{"scanning disabled", func(t *testing.T, db *database.DB) {
			require.NoError(t, db.SetSetting("update_scan_interval", "0"))
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, db, _ := newUpdateSettingsFixture(t)
			scheduled(t, db)
			tc.mutate(t, db)

			req := httptest.NewRequest(http.MethodGet, "/settings/updates", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			var response map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			if tc.present {
				assert.Contains(t, response, "nextApplyAt")
			} else {
				assert.NotContains(t, response, "nextApplyAt")
			}
		})
	}
}

// TestSettingsHandler_UpdateUpdateSettings_ScheduleEditDoesNotStopTheScheduler
// pins the re-arm's blast radius. The merged settings screen can send a
// schedule edit on its own, and routing the re-arm through Restart()/Stop()
// would take the scan scheduler down with it — the agent-os-mtbo.8 bug in a new
// place.
func TestSettingsHandler_UpdateUpdateSettings_ScheduleEditDoesNotStopTheScheduler(t *testing.T) {
	router, db, sched := newUpdateSettingsFixture(t)
	require.NoError(t, db.SetSetting("update_scan_interval", "60"))
	sched.Start(60 * time.Minute)
	require.True(t, sched.IsRunning())

	w := putUpdateSettings(t, router, `{"applyMode":"scheduled","applyTime":"05:15","applyDays":[2]}`)
	require.Equal(t, http.StatusOK, w.Code)

	assert.True(t, sched.IsRunning(), "a schedule-only edit must not stop the scan scheduler")
	assert.Eventually(t, func() bool {
		next := sched.NextApplyAt()
		return !next.IsZero() && next.Weekday() == time.Tuesday && next.Hour() == 5 && next.Minute() == 15
	}, time.Until(hangGuardDeadline(t)), 10*time.Millisecond,
		"a saved schedule must reach the running apply timer without a restart")
}

// TestSettingsHandler_GetScanDepth_LogsCauseOn500 is the seen-failing-first
// test for agent-os-ua4y: GetScanDepth wrote its 500 directly with c.JSON,
// bypassing handleError, so a GetSetting failure left no record beyond the
// middleware access line. faultyDB (faulty_db_test.go) forces GetSetting to
// fail with "sql: database is closed" (never sql.ErrNoRows), which is exactly
// the failure this handler's err != nil branch exists for.
//
// Two-sided per the brief: the log assertion is the one that flips from
// failing to passing across the fix; the status/code assertions are the
// control showing the routing change left the wire response unchanged.
func TestSettingsHandler_GetScanDepth_LogsCauseOn500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db := faultyDB(t)
	handler := NewSettingsHandler(db, "", "test-secret", false, nil, nil)
	router := gin.New()
	router.GET("/settings/scan-depth", handler.GetScanDepth)

	req := httptest.NewRequest(http.MethodGet, "/settings/scan-depth", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Control: status and error code must be unchanged by the routing fix.
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var appErr models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &appErr))
	require.Equal(t, "INTERNAL_ERROR", appErr.Code)

	// The assertion that must fail before the fix and pass after: an ERROR
	// line naming the cause must be emitted.
	if !strings.Contains(buf.String(), "database is closed") {
		t.Fatalf("500 emitted with no log of the underlying cause. captured = %q", buf.String())
	}
}
