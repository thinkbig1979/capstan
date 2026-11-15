package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-zp59: each refused backup action gives its own reason. One
// sentence, "it changes where backups go or what they keep", read oddly on a
// restore or a repository init.
func TestBackupLockedRefusal_ReasonPerAction_zp59(t *testing.T) {
	h, _ := newBackupHandlerH71i(t)
	for _, tc := range []struct {
		name, method, path string
		body               map[string]any
		want               string
	}{
		{"settings", http.MethodPut, "/api/settings/backup", map[string]any{"repository": "/attacker/repo"},
			"Unlock env values to change where backups go, their credentials or their retention: backups hold every stack's .env, so whoever picks the repository can read them"},
		{"restore", http.MethodPost, "/api/backups/restore", map[string]any{"stackId": "s1", "snapshotId": "abc123", "confirm": true},
			"Unlock env values to restore a backup: a restore writes backed-up files, secrets included, into your stacks"},
		{"dr-restore", http.MethodPost, "/api/backups/dr-restore", map[string]any{"confirm": true},
			"Unlock env values to run a disaster-recovery restore: it writes backed-up files, secrets included, into your stacks"},
		{"prune", http.MethodPost, "/api/backups/prune", map[string]any{"confirm": true},
			"Unlock env values to prune backups: pruning deletes backups"},
		{"repo init", http.MethodPost, "/api/backups/repo/init", nil,
			"Unlock env values to initialise a backup repository: the repository will hold every stack's .env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			lockedBackupRouterH71i(h).ServeHTTP(w, jsonReq(t, tc.method, tc.path, tc.body))
			require.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body.String())
			var body struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tc.want, body.Message)
		})
	}
}
