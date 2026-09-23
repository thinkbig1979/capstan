package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-oid3. checkUpdates' refresh branch reads the cache and the last scan
// time AFTER it has started a scan, so it degrades rather than refuses on a
// fault (agent-os-1gqn). It used to degrade by emitting the zero values, which
// made "could not read" identical on the wire to "empty cache, never scanned".
// Each fault arm hides ONE table, so the other read still succeeds and its key
// is asserted present first: a fixture that broke the whole response cannot
// satisfy the discriminating assertion.

// benignScanner starts nothing and reports no scan in flight: the
// ErrSchedulerStopping shape, which is the path where the client actually
// writes this body into its cache (scanning:false).
type benignScanner struct{}

func (benignScanner) StartBackgroundScan() error { return nil }
func (benignScanner) IsScanning() bool           { return false }

func refreshBody(t *testing.T, db *database.DB) (int, map[string]json.RawMessage) {
	t.Helper()
	handler := &ResourcesHandler{db: db, scheduler: benignScanner{}, actionLog: services.NewActionLogger(db)}
	router := setupResourcesRouter(handler)
	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates?refresh=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
	return w.Code, body
}

// FAULT ARM, cache. cached_updates is unreadable; settings still answer.
func TestCheckUpdatesRefresh_UnreadableCacheIsOmittedNotEmpty(t *testing.T) {
	db, hide, _ := hiddenTableDB(t, "cached_updates")
	hide()

	code, body := refreshBody(t, db)

	require.Equal(t, http.StatusAccepted, code, "the scan was started, so the refresh must still be accepted")
	require.Contains(t, body, "scanning", "precondition: the rest of the response must be intact")
	require.Contains(t, body, "scannedAt", "precondition: only the cache read faulted")

	for _, key := range []string{"updates", "fromCache"} {
		raw, present := body[key]
		require.False(t, present,
			"a GetCachedUpdates fault is being emitted as a fact: %s = %s, indistinguishable from "+
				"the genuinely empty cache TestCheckUpdatesRefresh_EmptyInstallStillReportsEmpty pins",
			key, string(raw))
	}
}

// FAULT ARM, last scan time. settings is unreadable; the cache still answers.
func TestCheckUpdatesRefresh_UnreadableScanTimeIsOmittedNotNever(t *testing.T) {
	db, hide, _ := hiddenTableDB(t, "settings")
	hide()

	code, body := refreshBody(t, db)

	require.Equal(t, http.StatusAccepted, code, "the scan was started, so the refresh must still be accepted")
	require.Contains(t, body, "updates", "precondition: only the settings read faulted")
	require.Contains(t, body, "fromCache", "precondition: only the settings read faulted")

	raw, present := body["scannedAt"]
	require.False(t, present,
		"an update_scan_last_run fault is being emitted as \"never scanned\": scannedAt = %s", string(raw))
}

// EMPTY ARM, the positive side of the same instrument. A healthy install with
// an empty cache and no scan yet must still SAY so; without this arm the fault
// arms are satisfied by a handler that never sends these keys at all.
func TestCheckUpdatesRefresh_EmptyInstallStillReportsEmpty(t *testing.T) {
	db, _, _ := hiddenTableDB(t, "cached_updates")

	code, body := refreshBody(t, db)

	require.Equal(t, http.StatusAccepted, code)
	require.JSONEq(t, `[]`, string(body["updates"]))
	require.JSONEq(t, `false`, string(body["fromCache"]))
	require.JSONEq(t, `""`, string(body["scannedAt"]))
}
