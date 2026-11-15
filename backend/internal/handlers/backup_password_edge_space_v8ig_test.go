package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-v8ig part 1 (Edwin, 2026-11-15). restic trims whitespace from the
// password it reads, so a stored password with spaces at either end works
// inside Capstan but a manual restore with the password as typed is refused.
// It is saved as typed, and the settings response flags it so the form can
// warn. The flag carries one bit, never the password.
func TestGetSettings_FlagsPasswordEdgeWhitespace_V8ig(t *testing.T) {
	cases := map[string]bool{
		" secret":    true,
		"secret ":    true,
		"\tsecret\n": true,
		"sec ret":    false,
		"secret":     false,
	}
	for pw, want := range cases {
		db := newBackupHandlerDB(t)
		require.NoError(t, db.SetSetting("restic_password", pw))
		svc := buildBackupSvc(t, db, true, false)
		h := NewBackupHandler(svc, db, slog.Default())
		t.Cleanup(h.Stop)

		w := httptest.NewRecorder()
		newBackupRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil))
		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)

		got, ok := body["passwordEdgeWhitespace"].(bool)
		require.True(t, ok, "passwordEdgeWhitespace must be a bool in the settings response (password %q)", pw)
		assert.Equal(t, want, got, "password %q", pw)
		assert.NotContains(t, w.Body.String(), "secret", "the password itself is never in the response")
	}
}
