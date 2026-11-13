package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The policy PUT reads the row to decide what the operator's request means,
// then writes. The auto-update pass writes the same row's failure count and
// pause flag (RecordAutoUpdatePolicyFailure, ResetAutoUpdatePolicyFailures).
// The PUT used to write its whole stale copy back with INSERT OR REPLACE, so a
// pause the pass recorded between the read and the write was erased and the
// failing container kept being retried (agent-os-nlll, review S7).

const nlllCreatedAt = "2020-01-01T00:00:00Z"

func newPolicyDBNlll(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedPolicyNlll(t *testing.T, db *database.DB, enabled bool, failures int, paused bool) *models.AutoUpdatePolicy {
	t.Helper()
	p := &models.AutoUpdatePolicy{
		ID:                  "policy-nlll",
		TargetType:          "container",
		TargetID:            "c1",
		Enabled:             enabled,
		ConsecutiveFailures: failures,
		Paused:              paused,
		CreatedAt:           nlllCreatedAt,
		UpdatedAt:           nlllCreatedAt,
	}
	require.NoError(t, db.UpsertAutoUpdatePolicy(p))
	return p
}

func putPolicyNlll(t *testing.T, db *database.DB, target, body string) (int, models.AutoUpdatePolicy) {
	t.Helper()
	r := gin.New()
	h := NewResourcesHandler(nil, db, nil)
	r.PUT("/api/resources/auto-update-policies/:targetType/:targetId", h.upsertAutoUpdatePolicy)
	req := httptest.NewRequest(http.MethodPut, "/api/resources/auto-update-policies/container/"+target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp models.AutoUpdatePolicy
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body: %s", w.Body.String())
	}
	return w.Code, resp
}

func setAfterReadHookNlll(t *testing.T, fn func()) {
	t.Helper()
	upsertAutoUpdatePolicyAfterReadHook = fn
	t.Cleanup(func() { upsertAutoUpdatePolicyAfterReadHook = nil })
}

func TestUpsertAutoUpdatePolicy_S7_PauseRecordedMidPUTSurvives(t *testing.T) {
	db := newPolicyDBNlll(t)
	seed := seedPolicyNlll(t, db, true, 2, false)

	// The pass's own writer, with the pass's own threshold, in the window.
	fired := false
	setAfterReadHookNlll(t, func() {
		written, paused, err := db.RecordAutoUpdatePolicyFailure(seed.ID, "2020-01-02T00:00:00Z", 3)
		require.NoError(t, err)
		require.True(t, written && paused, "positive side: the pass's third failure must pause the policy (written=%v paused=%v)", written, paused)
		fired = true
	})

	code, resp := putPolicyNlll(t, db, "c1", `{"enabled":true}`)
	require.True(t, fired, "the hook never ran, so the interleave was not tested")
	require.Equal(t, http.StatusOK, code)

	got, err := db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	require.True(t, got.Paused && got.ConsecutiveFailures == 3,
		"the pause the pass recorded during the PUT was lost: stored paused=%v consecutive_failures=%d, want paused=true consecutive_failures=3",
		got.Paused, got.ConsecutiveFailures)
	require.True(t, got.Enabled)
	require.Equal(t, seed.ID, got.ID)
	require.Equal(t, nlllCreatedAt, got.CreatedAt)

	// The response is the stored row, so it cannot report a pause as cleared.
	require.True(t, resp.Paused && resp.ConsecutiveFailures == 3,
		"response says paused=%v consecutive_failures=%d while the stored row is paused with 3 failures", resp.Paused, resp.ConsecutiveFailures)
}

func TestUpsertAutoUpdatePolicy_S7_OperatorChangeApplied(t *testing.T) {
	cases := []struct {
		name                    string
		seedEnabled             bool
		seedFailures            int
		seedPaused              bool
		body                    string
		wantEnabled, wantPaused bool
		wantFailures            int
	}{
		// A disabled row's counters are left as they are by an enable.
		{"enable a disabled row", false, 1, false, `{"enabled":true}`, true, false, 1},
		{"disable an enabled row", true, 2, false, `{"enabled":false}`, false, false, 0},
		{"disable a paused row", true, 3, true, `{"enabled":false}`, false, false, 0},
		{"resume a paused row", true, 3, true, `{"enabled":true}`, true, false, 0},
		{"re-enable an enabled row", true, 2, false, `{"enabled":true}`, true, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newPolicyDBNlll(t)
			seed := seedPolicyNlll(t, db, tc.seedEnabled, tc.seedFailures, tc.seedPaused)

			code, resp := putPolicyNlll(t, db, "c1", tc.body)
			require.Equal(t, http.StatusOK, code)

			got, err := db.GetAutoUpdatePolicy("container", "c1")
			require.NoError(t, err)
			require.Equal(t, tc.wantEnabled, got.Enabled, "enabled")
			require.Equal(t, tc.wantPaused, got.Paused, "paused")
			require.Equal(t, tc.wantFailures, got.ConsecutiveFailures, "consecutive_failures")
			require.Equal(t, seed.ID, got.ID, "the row keeps its id")
			require.Equal(t, nlllCreatedAt, got.CreatedAt, "the row keeps its created_at")
			require.Equal(t, *got, resp, "the response is the stored row")
		})
	}
}

func TestUpsertAutoUpdatePolicy_S7_AbsentRowIsCreated(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		db := newPolicyDBNlll(t)
		body := `{"enabled":false}`
		if enabled {
			body = `{"enabled":true}`
		}
		code, resp := putPolicyNlll(t, db, "fresh", body)
		require.Equal(t, http.StatusOK, code)

		got, err := db.GetAutoUpdatePolicy("container", "fresh")
		require.NoError(t, err, "PUT must create an absent policy")
		require.NotEmpty(t, got.ID)
		require.Equal(t, enabled, got.Enabled)
		require.False(t, got.Paused)
		require.Equal(t, 0, got.ConsecutiveFailures)
		require.Equal(t, *got, resp, "the response is the stored row")
	}
}
