package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestResourcesHandler_CheckUpdates_RefreshWhileSchedulerStopping_NoServerError
// covers the regression from agent-os-mtbo.9: checkUpdates used to compare the
// StartBackgroundScan error against the literal string "scan already in
// progress" and turn everything else into a 500, so once the scheduler learned
// to refuse scans during its stop window, clicking Refresh at the wrong moment
// returned a server error. The stop window is not shutdown-only — Restart()
// goes through Stop(), and Restart() is what an interval change in the
// settings UI triggers.
//
// Start(time.Hour) so no tick can fire during the test; IsRunning is asserted
// directly rather than inferred from a side effect.
func TestResourcesHandler_CheckUpdates_RefreshWhileSchedulerStopping_NoServerError(t *testing.T) {
	handler, sched, checker := newTestResourcesHandlerWithScheduler(t)
	t.Cleanup(func() { close(checker.block) })
	router := setupResourcesRouter(handler)

	sched.Start(time.Hour)
	require.True(t, sched.IsRunning(), "scheduler must be running before we stop it")
	sched.Stop()
	require.False(t, sched.IsRunning(), "Stop must have latched the scheduler down")

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates?refresh=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code,
		"a refresh landing in the scheduler's stop window must degrade to the cached answer, not 500")

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	scanning, ok := response["scanning"].(bool)
	require.True(t, ok, "response must carry a boolean scanning field")
	assert.False(t, scanning,
		"no scan was started, so reporting scanning=true would have the client poll for a result that never arrives")
	_, hasUpdates := response["updates"]
	assert.True(t, hasUpdates, "the refresh must still hand back the cached updates list")
}

// TestScanStartIsBenign is the other half of the pairing: the stop-window case
// above only proves that some errors no longer 500. This proves the handler
// has not simply started swallowing everything — an unrecognised error is
// still classified as a real failure and still takes the 500 branch.
func TestScanStartIsBenign(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, true},
		{"scan already in progress", services.ErrScanInProgress, true},
		{"scheduler stopping", services.ErrSchedulerStopping, true},
		{"wrapped sentinel", fmt.Errorf("start background scan: %w", services.ErrSchedulerStopping), true},
		{"unexpected failure still fails", errors.New("database is unreachable"), false},
		{"lookalike text is not a sentinel", errors.New("scheduler is stopping"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scanStartIsBenign(tc.err))
		})
	}
}
