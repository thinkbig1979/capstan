package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-r1kc, the DISPLAY half. GetLogRetention reads the same accessor
// PruneHistory does, so before this bead a persistent settings fault made the
// settings page show 90 as the configured retention — agreeing with the prune
// that had just truncated every history to 90 days. The delete half and the
// display half have to move together: fixing only the delete leaves the operator
// with no signal anywhere that the setting could not be read, which is what made
// this defect invisible.
//
// The fixture is hiddenTableDB (discarded_getter_dbfault_test.go:54), whose own
// two-sided control is TestHiddenTableDB_FaultsOneTableAndNotTheOthers: it
// renames ONE table through a side connection, so the fault arrives as
// "no such table: settings" — not sql.ErrNoRows, which is exactly the branch
// this fix has to discriminate.

// newRetentionFaultRouter registers the retention route only. newSettingsRouter next
// door registers three unrelated routes and not this one; a separate helper
// keeps this file revertible on its own, which is what a per-file mutation
// control needs.
func newRetentionFaultRouter(t *testing.T, db *database.DB) *gin.Engine {
	t.Helper()
	h := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil, &config.Config{})
	return newDBFaultRouter(func(r *gin.Engine) {
		r.GET("/api/settings/log-retention", h.GetLogRetention)
	})
}

func TestGetLogRetention_FaultRefusesInsteadOfDisplayingTheDefault(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	require.NoError(t, db.SetSetting(database.SettingLogRetentionDays, "3650"),
		"seed the compliance retention this test is about")
	hide()
	r := newRetentionFaultRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/log-retention", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: the settings page is serving an invented retention while the configured 3650 could not be read. body = %s",
			w.Code, w.Body.String())
	}
	// The specific harm, asserted on the payload rather than on the status: the
	// number the operator sees must not be the fabricated default.
	require.NotContains(t, w.Body.String(), `"retentionDays":90`,
		"the response still displays the default as if it were the configured retention")
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read retention setting "max_log_retention_days"`,
		"GET /api/settings/log-retention with an unreadable settings table")
}

// CONTROL: the same route, same router, healthy database. This is the arm that
// goes red if the refusal above is really "this endpoint has stopped working",
// and the arm that goes red if the conversion turned absence into refusal — the
// migration seeds all three keys, so a healthy DB exercises the present-row path
// and the response must carry the configured value, not a 500.
func TestGetLogRetention_HealthyDatabaseStillServesTheConfiguredValue(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, _, _ := hiddenTableDB(t, "settings")
	require.NoError(t, db.SetSetting(database.SettingLogRetentionDays, "3650"))
	r := newRetentionFaultRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/log-retention", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"retentionDays":3650`)
	require.Contains(t, w.Body.String(), `"minRetentionDays":7`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/settings/log-retention on a healthy database")
}
