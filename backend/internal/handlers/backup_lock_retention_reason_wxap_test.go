package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-wxap item 3: a locked save that only changes the retention was
// refused with the repository's reason ("whoever picks the repository can
// read them"), which does not fit. Retention alone gets its own action and
// reason; a body that also touches the destination or a credential keeps the
// repository's.
func TestBackupLockedRefusal_RetentionHasItsOwnReason_wxap(t *testing.T) {
	h, _ := newBackupHandlerH71i(t)
	const retention = "Unlock env values to change how long backups are kept: a shorter retention lets the next prune delete backups"
	const destination = "Unlock env values to change where backups go, their credentials or their retention: backups hold every stack's .env, so whoever picks the repository can read them"
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"keepDaily", map[string]any{"keepDaily": 1}, retention},
		{"autoPrune", map[string]any{"autoPrune": true}, retention},
		{"retention and schedule", map[string]any{"keepYearly": 0, "keepWeekly": 1, "hostname": "h"}, retention},
		{"retention and repository", map[string]any{"keepDaily": 1, "repository": "/attacker/repo"}, destination},
		{"password", map[string]any{"password": "x"}, destination},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			lockedBackupRouterH71i(h).ServeHTTP(w, jsonReq(t, http.MethodPut, "/api/settings/backup", tc.body))
			require.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body.String())
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "ENV_LOCKED", body.Code)
			assert.Equal(t, tc.want, body.Message)
		})
	}
}
