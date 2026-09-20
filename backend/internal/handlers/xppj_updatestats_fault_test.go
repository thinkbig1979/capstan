package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// agent-os-xppj. GetUpdateSettings read three counters, CHECKED the error and
// then discarded it by logging, with no return. On a fault all three stayed at
// their zero value and were emitted inside a 200, so "the database could not
// count" rendered as "0 containers with auto-update enabled, 0 updates in the
// last 7 days, 0 in the last 30" — the identical failure mode to agent-os-ufj7,
// reached by the third syntactic route (CHECKED-THEN-LOGGED), which neither the
// geterrors analyzer nor errcheck can see.
//
// The remedy is NOT ufj7's remedy. ufj7 returned the error because the probe WAS
// the answer; here the counters are one ancillary block of a response whose
// primary payload — the update-settings FORM — was read correctly a few lines
// above. Refusing the whole request would delete a correct, populated
// write-back form from the operator's screen, which is the harm agent-os-wczm,
// agent-os-fxhl and agent-os-ptiq exist to prevent. The handler already
// degrades partially inside a 200 one field away (settings.go, the
// ParseWeekdays failure reports the stored days as empty rather than refusing).
//
// So the block is OMITTED instead: the server stops asserting a number it
// cannot vouch for, which is ufj7's convention — absent, never wrong.
//
// The two arms below are the discrimination the bead asks for: a fault and a
// genuinely idle installation must not look the same on the wire.

func getUpdateSettingsBody(t *testing.T, db *database.DB) (int, map[string]json.RawMessage) {
	t.Helper()
	r := newSettingsRouter(t, db)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/updates", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
	return w.Code, body
}

// FAULT ARM. auto_update_policies is unreadable, so GetUpdateStats' first query
// fails. Every settings row still reads normally, so the form itself is intact
// and the request must still succeed — it is only the counts that are unknown.
func TestGetUpdateSettings_UnreadableStatsAreOmittedNotZeroed(t *testing.T) {
	db, hide, _ := hiddenTableDB(t, "auto_update_policies")
	hide()

	code, body := getUpdateSettingsBody(t, db)

	require.Equal(t, http.StatusOK, code,
		"the settings form read cleanly; only the counters faulted, so the form must still be served")
	require.Contains(t, body, "applyMode", "precondition: the rest of the response must be intact")

	raw, present := body["autoUpdateStats"]
	require.False(t, present,
		"a GetUpdateStats fault is being emitted as a factual count. autoUpdateStats = %s — "+
			"indistinguishable from a genuinely idle installation, which is what "+
			"TestGetUpdateSettings_IdleInstallationStillReportsZero proves this server also sends",
		string(raw))
}

// IDLE ARM, the positive side of the same instrument. A healthy database with no
// auto-update policies and no update history genuinely counts zero, and must
// still SAY zero. Without this arm the fault arm above is satisfied by a handler
// that never sends the block at all, which is worse than the bug.
func TestGetUpdateSettings_IdleInstallationStillReportsZero(t *testing.T) {
	db, err := database.NewWithMigrations(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	code, body := getUpdateSettingsBody(t, db)

	require.Equal(t, http.StatusOK, code)
	raw, present := body["autoUpdateStats"]
	require.True(t, present, "a healthy, idle installation must still report its counts")

	var stats struct {
		EnabledContainers int `json:"enabledContainers"`
		UpdatesLast7Days  int `json:"updatesLast7Days"`
		UpdatesLast30Days int `json:"updatesLast30Days"`
	}
	require.NoError(t, json.Unmarshal(raw, &stats))
	require.Equal(t, 0, stats.EnabledContainers)
	require.Equal(t, 0, stats.UpdatesLast7Days)
	require.Equal(t, 0, stats.UpdatesLast30Days)
}
