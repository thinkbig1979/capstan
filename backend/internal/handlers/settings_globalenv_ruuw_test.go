package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-ruuw: GET /settings/global-env sent {key, value} only, so the page
// masked by key name and showed an unlocked session a secret-shaped value
// under a harmless key in full. Each row now says whether it is sensitive, by
// the rule the locked view blanks on.

// getGlobalVarRows decodes the global env rows with their flags.
func getGlobalVarRows(t *testing.T, router http.Handler, path string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Vars []map[string]any `json:"vars"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Vars
}

func TestGlobalEnvRuuw_UnlockedGetReportsSensitive(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	writeGlobalEnvFile(t, dataDir,
		"TLS=\"-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----\"\n"+
			"NOTES=https://ruuwuser:ruuwpass@example.com\n"+
			"TZ=UTC\n")

	rows := getGlobalVarRows(t, router, "/unlocked")
	require.Len(t, rows, 3)
	assert.Equal(t, "TLS", rows[0]["key"])
	assert.Equal(t, true, rows[0]["sensitive"], "a private key under a harmless key")
	assert.Equal(t, "NOTES", rows[1]["key"])
	assert.Equal(t, true, rows[1]["sensitive"], "a URL credential under a harmless key")
	assert.Equal(t, "TZ", rows[2]["key"])
	assert.NotContains(t, rows[2], "sensitive", "a plain value carries no flag")
}

// The flag is the locked view's rule: a row is sensitive exactly when a locked
// GET blanks its value (or its key, for a hidden body row).
func TestGlobalEnvRuuw_FlagMatchesLockedBlanking(t *testing.T) {
	const content = "# shared\n" +
		"DB_PASSWORD=pw1\n" +
		"TZ=UTC\n" +
		"BARE\n" +
		"API_TOKEN: tok\n" +
		"COLON: plain\n" +
		"NOTES=\"a\" DB_SECRET=x\n" +
		"CERT=\"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\"\n" +
		"URL=postgres://u:p@db/x\n" +
		"export EXP_TOKEN=e\n" +
		"PLAIN=\"quoted\" # note\n"
	dataDir, router := globalEnvTestRouter(t)
	writeGlobalEnvFile(t, dataDir, content)

	unlocked := getGlobalVarRows(t, router, "/unlocked")
	locked := getGlobalVarRows(t, router, "/locked")
	require.Len(t, locked, len(unlocked))
	sawSensitive, sawPlain := false, false
	for i, u := range unlocked {
		blanked := locked[i]["key"] == "" || (locked[i]["value"] == "" && u["value"] != "")
		sensitive := u["sensitive"] == true
		assert.Equal(t, blanked, sensitive, "row %d %v: locked blanks it = %v, unlocked flag = %v", i, u["key"], blanked, sensitive)
		assert.Equal(t, sensitive, locked[i]["sensitive"] == true, "row %d: the locked GET carries the same flag", i)
		sawSensitive = sawSensitive || sensitive
		sawPlain = sawPlain || !sensitive
	}
	assert.True(t, sawSensitive && sawPlain, "the fixture has rows on both sides")
}
